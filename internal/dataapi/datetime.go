// Package dataapi renders database values as Data API fields.
//
// The datetime helpers reproduce the formatting the real Data API uses: a
// timestamp keeps at most six fractional digits and a time at most three, values
// carrying a UTC offset are shifted to UTC, and trailing zeros are trimmed off
// the fractional seconds along with the decimal point once nothing is left.
package dataapi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	datetimeLayout = "2006-01-02 15:04:05.000000"
	timeLayout     = "15:04:05.000"
)

// offsetPattern matches a trailing UTC offset such as +02, -0230 or +02:30:15.
var offsetPattern = regexp.MustCompile(`([+-])(\d{2})(?::?(\d{2}))?(?::?(\d{2}))?$`)

// datetimeFractionPattern keeps at most six fractional digits.
var datetimeFractionPattern = regexp.MustCompile(`^[^.]+\.\d{1,6}|^[^.]+`)

// timeFractionPattern keeps at most three fractional digits.
var timeFractionPattern = regexp.MustCompile(`^[^.]+\.\d{1,3}|^[^.]+`)

// StripNanoSecs removes trailing zeros from the fractional seconds, and the
// decimal point too once nothing is left after it.
func StripNanoSecs(value string) string {
	if !strings.Contains(value, ".") {
		return value
	}
	parts := strings.SplitN(value, ".", 2)
	fraction := strings.TrimRight("."+parts[1], "0.")
	return parts[0] + fraction
}

// FormatDatetime renders a timestamp without timezone, keeping up to six
// fractional digits.
func FormatDatetime(value string) string {
	return StripNanoSecs(datetimeFractionPattern.FindString(value))
}

// FormatTime renders a time without timezone, keeping up to three fractional
// digits.
func FormatTime(value string) string {
	return StripNanoSecs(timeFractionPattern.FindString(value))
}

// FormatDatetimeValue renders a time.Time the way FormatDatetime renders a
// string. The instant is used as-is, without a timezone conversion.
func FormatDatetimeValue(t time.Time) string {
	return StripNanoSecs(t.Format(datetimeLayout))
}

// ConvertOffsetDatetimeToUTC shifts a timestamp carrying a UTC offset into UTC
// and renders it with up to six fractional digits.
func ConvertOffsetDatetimeToUTC(value string) (string, error) {
	base, offsetSeconds, ok := splitOffset(value)
	if !ok {
		// No offset present: the value is already an instant we can render.
		return FormatDatetime(value), nil
	}

	parsed, err := parseNaiveDatetime(base)
	if err != nil {
		return "", err
	}
	utc := parsed.Add(-time.Duration(offsetSeconds) * time.Second)
	return StripNanoSecs(utc.Format(datetimeLayout)), nil
}

// ConvertOffsetDatetimeValueToUTC is the time.Time form of
// ConvertOffsetDatetimeToUTC, for drivers that decode timestamps for us.
func ConvertOffsetDatetimeValueToUTC(t time.Time) string {
	return StripNanoSecs(t.UTC().Format(datetimeLayout))
}

// ConvertOffsetTimeToUTC shifts a time-of-day carrying a UTC offset into UTC and
// renders it with up to three fractional digits. The result wraps within the
// day, matching java.time.OffsetTime.
func ConvertOffsetTimeToUTC(value string) (string, error) {
	base, offsetSeconds, ok := splitOffset(value)
	if !ok {
		return FormatTime(value), nil
	}

	nanosOfDay, err := parseNanosOfDay(base)
	if err != nil {
		return "", err
	}

	const nanosPerDay = int64(24 * 60 * 60 * 1e9)
	nanosOfDay -= int64(offsetSeconds) * int64(time.Second)
	nanosOfDay = ((nanosOfDay % nanosPerDay) + nanosPerDay) % nanosPerDay

	utc := time.Unix(0, nanosOfDay).UTC()
	return StripNanoSecs(utc.Format(timeLayout)), nil
}

// splitOffset separates a trailing UTC offset from the value in front of it.
func splitOffset(value string) (base string, offsetSeconds int, ok bool) {
	match := offsetPattern.FindStringSubmatchIndex(value)
	if match == nil {
		return value, 0, false
	}

	base = value[:match[0]]
	// Guard against reading the trailing "-10" of a plain date as an offset.
	if !strings.Contains(base, ":") {
		return value, 0, false
	}

	groups := offsetPattern.FindStringSubmatch(value)
	hours, _ := strconv.Atoi(groups[2])
	minutes := 0
	seconds := 0
	if groups[3] != "" {
		minutes, _ = strconv.Atoi(groups[3])
	}
	if groups[4] != "" {
		seconds, _ = strconv.Atoi(groups[4])
	}

	offsetSeconds = hours*3600 + minutes*60 + seconds
	if groups[1] == "-" {
		offsetSeconds = -offsetSeconds
	}
	return base, offsetSeconds, true
}

var naiveDatetimeLayouts = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
}

func parseNaiveDatetime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	for _, layout := range naiveDatetimeLayouts {
		if parsed, err := time.ParseInLocation(layout, trimmed, time.UTC); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp %q", value)
}

// parseNanosOfDay reads HH:MM:SS[.fraction] as nanoseconds since midnight.
func parseNanosOfDay(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	fields := strings.SplitN(trimmed, ":", 3)
	if len(fields) != 3 {
		return 0, fmt.Errorf("cannot parse time %q", value)
	}

	hours, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("cannot parse time %q", value)
	}
	minutes, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("cannot parse time %q", value)
	}

	secondsPart := fields[2]
	fractionNanos := int64(0)
	if dot := strings.IndexByte(secondsPart, '.'); dot >= 0 {
		fraction := secondsPart[dot+1:]
		secondsPart = secondsPart[:dot]
		if len(fraction) > 9 {
			fraction = fraction[:9]
		}
		digits, err := strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("cannot parse time %q", value)
		}
		for i := len(fraction); i < 9; i++ {
			digits *= 10
		}
		fractionNanos = digits
	}

	seconds, err := strconv.Atoi(secondsPart)
	if err != nil {
		return 0, fmt.Errorf("cannot parse time %q", value)
	}

	return int64(hours)*int64(time.Hour) +
		int64(minutes)*int64(time.Minute) +
		int64(seconds)*int64(time.Second) +
		fractionNanos, nil
}
