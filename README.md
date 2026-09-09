# local-data-api

[![Go CI](https://github.com/sonnh96/local-data-api/actions/workflows/ci.yml/badge.svg)](https://github.com/sonnh96/local-data-api/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/sonnh96/local-data-api/branch/main/graph/badge.svg)](https://codecov.io/gh/sonnh96/local-data-api)
![license](https://img.shields.io/github/license/sonnh96/local-data-api.svg)

A local stand-in for the **AWS Aurora Serverless Data API**, written in Go.

It speaks the same HTTP protocol as the real service, so the AWS SDKs, boto3 and the AWS
CLI talk to it unchanged — point them at `http://localhost:8080` and your tests run
against a real MySQL or PostgreSQL server instead of a cloud endpoint.

## What is the Data API?

<https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/data-api.html>

## How it works

local-data-api is a proxy in front of a real database. It accepts Data API requests,
turns the `:name` bind parameters into driver placeholders, runs the statement, and
renders the result as Data API fields — applying the same JDBC type mapping the real
service uses, so a `BIGINT` comes back as `longValue`, a `bytea` as `blobValue`, and a
`timestamptz` as a UTC string.

## Supported databases

- MySQL
- PostgreSQL

## Quick start

```bash
docker run --rm -it --name my-data-api -p 8080:80 \
  -e ENGINE=MySQL \
  -e MYSQL_HOST=<YOUR_MYSQL_HOST> \
  -e MYSQL_PORT=<YOUR_MYSQL_PORT> \
  -e MYSQL_USER=<YOUR_MYSQL_USER> \
  -e MYSQL_PASSWORD=<YOUR_MYSQL_PASS> \
  -e RESOURCE_ARN=arn:aws:rds:us-east-1:123456789012:cluster:dummy \
  -e SECRET_ARN=arn:aws:secretsmanager:us-east-1:123456789012:secret:dummy \
  sonnh96/local-data-api
```

Then point any AWS client at it:

```bash
aws --endpoint-url http://127.0.0.1:8080 rds-data execute-statement \
  --resource-arn "arn:aws:rds:us-east-1:123456789012:cluster:dummy" \
  --secret-arn "arn:aws:secretsmanager:us-east-1:123456789012:secret:dummy" \
  --database 'test' \
  --sql "show databases"
```

### docker compose

```bash
docker compose -f docker-compose-mysql.yml up --build     # MySQL
docker compose -f docker-compose-postgres.yml up --build  # PostgreSQL
```

Both files start a database alongside the API and wait for it to be healthy.

### With boto3

```python
import boto3

client = boto3.client(
    'rds-data',
    endpoint_url='http://127.0.0.1:8080',
    aws_access_key_id='aaa',
    aws_secret_access_key='bbb',
    region_name='us-east-1',
)

client.execute_statement(
    resourceArn='arn:aws:rds:us-east-1:123456789012:cluster:dummy',
    secretArn='arn:aws:secretsmanager:us-east-1:123456789012:secret:dummy',
    database='test',
    sql='select * from users',
)
```

```python
{'numberOfRecordsUpdated': 0,
 'records': [[{'longValue': 1}, {'stringValue': 'ichiro'}, {'longValue': 17}],
             [{'longValue': 2}, {'stringValue': 'ken'}, {'longValue': 20}],
             [{'longValue': 3}, {'stringValue': 'lisa'}, {'isNull': True}]]}
```

On PostgreSQL, list databases with `SELECT datname FROM pg_database` rather than
`show databases`.

## Configuration

| Variable            | Default                                                      | Meaning                                                                                                        |
| ------------------- | ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| `ENGINE`            | `MySQL`                                                      | Any value containing `mysql` selects MySQL; anything else selects PostgreSQL.                                     |
| `RESOURCE_ARN`      | `arn:aws:rds:us-east-1:123456789012:cluster:dummy`           | The cluster ARN clients must send.                                                                                |
| `SECRET_ARN`        | `arn:aws:secretsmanager:us-east-1:123456789012:secret:dummy` | The secret ARN clients must send.                                                                                 |
| `PORT`              | `80`                                                         | The port the API listens on.                                                                                      |
| `MYSQL_HOST`        | `127.0.0.1`                                                  | MySQL server host.                                                                                                |
| `MYSQL_PORT`        | `3306`                                                       | MySQL server port.                                                                                                |
| `MYSQL_USER`        | `root`                                                       | MySQL user the secret ARN resolves to.                                                                            |
| `MYSQL_PASSWORD`    | `example`                                                    | MySQL password.                                                                                                   |
| `POSTGRES_HOST`     | `127.0.0.1`                                                  | PostgreSQL server host.                                                                                           |
| `POSTGRES_PORT`     | `5432`                                                       | PostgreSQL server port.                                                                                           |
| `POSTGRES_USER`     | `postgres`                                                   | PostgreSQL user the secret ARN resolves to.                                                                       |
| `POSTGRES_PASSWORD` | `example`                                                    | PostgreSQL password.                                                                                              |
| `POSTGRES_SSLMODE`  | `disable`                                                    | `sslmode` for the PostgreSQL connection.                                                                          |

## Endpoints

| Method + path               | Notes                                                |
| --------------------------- | ---------------------------------------------------- |
| `POST /Execute`             | Run one statement, with optional `:name` parameters. |
| `POST /BatchExecute`        | Run one statement once per parameter set.            |
| `POST /BeginTransaction`    | Open a transaction and return its ID.                |
| `POST /CommitTransaction`   | Commit and release the transaction.                  |
| `POST /RollbackTransaction` | Roll back and release the transaction.               |
| `POST /ExecuteSql`          | Not implemented; returns 404.                        |

## Bind parameters

Named parameters use `:name`, as on the real service:

```json
{
  "sql": "INSERT INTO users (name, age) VALUES (:name, :age)",
  "parameters": [
    {"name": "name", "value": {"stringValue": "ichiro"}},
    {"name": "age",  "value": {"longValue": 17}}
  ]
}
```

The parser understands string literals, quoted identifiers, comments, dollar-quoted
bodies and PostgreSQL's `::` cast operator, so `SELECT :value::text` binds correctly.

`typeHint` accepts `DATE`, `TIME`, `TIMESTAMP`, `DECIMAL`, `UUID` and `JSON`, and a
malformed value is rejected with a 400 naming the parameter.

### Inserting arrays into PostgreSQL

The Data API cannot bind an array through `SqlParameter`. Pass it as a string and cast:

```sql
insert into cfg.attributes(id, type, attributes)
values(:id, :type, cast(:attributes as text[]));
```

where `attributes` is a Postgres array literal such as `{"Volume","Material"}`.
Array *columns* are read back as `arrayValue` normally.

## Development

Requires Go 1.24+.

```bash
make test     # go test -race ./...
make cover    # coverage summary
make build    # bin/local-data-api
make run      # run against a local database
make lint     # gofmt + go vet
make docker   # build the image

./scripts/integration-test.sh   # AWS CLI end-to-end against MySQL and PostgreSQL
```

### Layout

| Package             | Responsibility                                              |
| ------------------- | ----------------------------------------------------------- |
| `cmd/local-data-api` | Entry point: config, HTTP server, graceful shutdown.        |
| `internal/server`   | Routing and error rendering.                                 |
| `internal/models`   | Request and response payloads.                               |
| `internal/apierr`   | Data API error types and their HTTP status codes.            |
| `internal/secrets`  | The fake Secrets Manager.                                    |
| `internal/resource` | Resource registry, connection pools, transactions, execution.|
| `internal/dataapi`  | Rendering database values as Data API fields.                |
| `internal/jdbc`     | `java.sql.Types` constants, mapped from driver type names.   |
| `internal/sqlparam` | `:name` parameter rewriting and statement-kind detection.    |
| `internal/config`   | Reading the environment.                                     |
| `internal/testdb`   | An in-process driver, so unit tests need no database.        |

## Known limitations

- **`columnMetadata` is thinner than the real service's.** Go's `database/sql` does not
  expose what JDBC's `ResultSetMetaData` does, so `tableName`, `schemaName`,
  `isAutoIncrement`, `isCaseSensitive` and `isCurrency` report their zero value.
  `name`, `label`, `type`, `typeName`, `nullable`, `precision`, `scale` and `isSigned`
  are populated.
- **Query vs. update is decided from the leading keyword**, because `database/sql` needs
  `Query` or `Exec` chosen before the statement runs. `SELECT`, `WITH`, `SHOW`,
  `DESCRIBE`, `EXPLAIN`, `VALUES`, `TABLE`, `CALL` and any statement with a `RETURNING`
  clause are queries; everything else is an update.
- **A MySQL `TINYINT(1)` reports `longValue`**, not `booleanValue`: the driver does not
  expose the display width needed to tell `TINYINT(1)` from `TINYINT(4)`.
- **`BatchExecute` runs the statement once per parameter set**, so `updateResults`
  carries the generated key of each individual execution.

## Contributing

```bash
git clone git@github.com:<your username>/local-data-api.git
cd local-data-api

git checkout -b new-branch
make test
./scripts/integration-test.sh
```

## Related projects

### py-data-api

Data API client for Python — <https://github.com/koxudaxi/py-data-api>

## Links

- Docker image — <https://hub.docker.com/r/sonnh96/local-data-api>
- Source — <https://github.com/sonnh96/local-data-api>
- Documentation — <https://sonnh96.github.io/local-data-api>

## License

local-data-api is released under the MIT License. <http://www.opensource.org/licenses/mit-license>
