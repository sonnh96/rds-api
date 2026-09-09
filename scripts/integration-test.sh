#!/usr/bin/env bash
set -ex

#
# End-to-end test with the AWS CLI, against PostgreSQL and MySQL.
#
# These assertions check the wire format the AWS SDKs depend on. More detailed
# cases belong in the Go unit tests.
#

cd "$(dirname "$0")/.."

export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-dummy}
export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-dummy}
export AWS_DEFAULT_REGION=${AWS_DEFAULT_REGION:-us-east-1}

RESOURCE_ARN=arn:aws:rds:us-east-1:123456789012:cluster:dummy
SECRET_ARN=arn:aws:secretsmanager:us-east-1:123456789012:secret:dummy
ENDPOINT=http://localhost:8080

compose() {
    db=$1; shift
    docker compose -f "./docker-compose-${db}.yml" "$@"
}

down() {
    db=$1
    [ -n "$db" ] && compose "$db" down -v || true
}

wait_for_api() {
    for _ in $(seq 1 60); do
        if curl -sf -o /dev/null -X POST "${ENDPOINT}/BeginTransaction" \
            -H 'Content-Type: application/json' \
            -d "{\"resourceArn\":\"${RESOURCE_ARN}\",\"secretArn\":\"${SECRET_ARN}\",\"database\":\"test\"}"; then
            return 0
        fi
        sleep 2
    done
    echo "local-data-api did not become ready" >&2
    return 1
}

statement() {
    aws rds-data execute-statement \
        --endpoint-url "$ENDPOINT" \
        --database 'test' \
        --resource-arn "$RESOURCE_ARN" \
        --secret-arn "$SECRET_ARN" \
        --include-result-metadata \
        --sql "$1"
}

run_tests() {
    db=$1

    # select text
    statement "SELECT 'hello' AS value" | jq -e '.records[0][0].stringValue == "hello"'

    # select number
    statement 'SELECT 1 AS value' | jq -e '.records[0][0].longValue == 1'

    if [ "$db" = "postgres" ]; then
        statement 'SELECT 1 > 0 AS value'          | jq -e '.records[0][0].booleanValue == true'
        statement "SELECT 'hello'::bytea as value" | jq -e '.records[0][0].blobValue == "aGVsbG8="'
        statement "SELECT CAST('2021-03-10 22:41:04.068123+02' AS TIMESTAMPTZ) AS value" \
            | jq -e '.records[0][0].stringValue == "2021-03-10 20:41:04.068123"'
        statement 'SELECT CAST(null AS TIMESTAMPTZ) AS value' | jq -e '.records[0][0].isNull == true'
        # Arrays, and the :: cast operator in a bound parameter.
        statement 'SELECT ARRAY[1,2,3] AS value' | jq -e '.records[0][0].arrayValue.longValues == [1,2,3]'
    fi

    if [ "$db" = "mysql" ]; then
        # Booleans are just numbers in MySQL.
        statement 'SELECT 1 > 0 AS value'          | jq -e '.records[0][0].longValue == 1'
        statement "SELECT BINARY 'hello' as value" | jq -e '.records[0][0].blobValue == "aGVsbG8="'
        statement "SELECT CAST('2021-03-10 22:41:04.968123' AS DATETIME) AS value" \
            | jq -e '.records[0][0].stringValue == "2021-03-10 22:41:05"'
        statement 'SELECT CAST(null AS DATETIME) AS value' | jq -e '.records[0][0].isNull == true'
    fi

    # A transaction must survive the request that opened it, commit, and then be gone.
    transaction_id=$(aws rds-data begin-transaction \
        --endpoint-url "$ENDPOINT" --database 'test' \
        --resource-arn "$RESOURCE_ARN" --secret-arn "$SECRET_ARN" \
        --query transactionId --output text)

    aws rds-data execute-statement \
        --endpoint-url "$ENDPOINT" --database 'test' \
        --resource-arn "$RESOURCE_ARN" --secret-arn "$SECRET_ARN" \
        --transaction-id "$transaction_id" \
        --sql 'CREATE TABLE integration_test (id int)' > /dev/null

    aws rds-data commit-transaction \
        --endpoint-url "$ENDPOINT" \
        --resource-arn "$RESOURCE_ARN" --secret-arn "$SECRET_ARN" \
        --transaction-id "$transaction_id" \
        | jq -e '.transactionStatus == "Transaction Committed"'

    statement 'SELECT count(*) AS value FROM integration_test' | jq -e '.records[0][0].longValue == 0'

    # Named parameters, in a batch.
    aws rds-data batch-execute-statement \
        --endpoint-url "$ENDPOINT" --database 'test' \
        --resource-arn "$RESOURCE_ARN" --secret-arn "$SECRET_ARN" \
        --sql 'INSERT INTO integration_test (id) VALUES (:id)' \
        --parameter-sets '[[{"name":"id","value":{"longValue":1}}],[{"name":"id","value":{"longValue":2}}]]' > /dev/null

    statement 'SELECT count(*) AS value FROM integration_test' | jq -e '.records[0][0].longValue == 2'
}

trap 'down mysql; down postgres' EXIT

for db in mysql postgres; do
    down "$db"
    compose "$db" up --build --force-recreate -d
    wait_for_api
    run_tests "$db"
    echo " ✅ ${db} E2E completed successfully"
    down "$db"
done
