package filters

import (
	"fmt"
	"time"
)

func registerDateFilters() {
	Register("date", formatDate)
	Register("dateFormat", dateFormat)
	Register("timeAgo", timeAgo)
	Register("timeFrom", timeFrom)
	Register("dateAdd", dateAdd)
	Register("dateSub", dateSub)
	Register("dateDiff", dateDiff)
	Register("dateCompare", dateCompare)
	Register("now", now)
	Register("today", today)
	Register("tomorrow", tomorrow)
	Register("yesterday", yesterday)
	Register("weekday", weekday)
	Register("month", month)
	Register("year", year)
	Register("day", day)
	Register("hour", hour)
	Register("minute", minute)
	Register("second", second)
	Register("unix", unix)
	Register("fromUnix", fromUnix)
	Register("iso8601", iso8601)
	Register("rfc3339", rfc3339)
	Register("humanDuration", humanDuration)
	Register("durationSeconds", durationSeconds)
	Register("timezone", timezone)
}

const (
	defaultDateFormat = "2006-01-02 15:04:05"
	humanDateFormat   = "January 2, 2006"
	shortDateFormat   = "Jan 2, 2006"
)

func parseTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		formats := []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02T15:04:05Z",
			"2006-01-02 15:04:05",
			"2006-01-02",
			"01/02/2006",
			"02/01/2006",
		}

		for _, format := range formats {
			if parsed, err := time.Parse(format, t); err == nil {
				return parsed, nil
			}
		}

		return time.Time{}, fmt.Errorf("unable to parse time: %s", t)
	case int64:
		return time.Unix(t, 0), nil
	case int:
		return time.Unix(int64(t), 0), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time type: %T", t)
	}
}

func formatDate(v any, format ...string) string {
	t, err := parseTime(v)
	if err != nil {
		return ""
	}

	f := defaultDateFormat
	if len(format) > 0 && format[0] != "" {
		f = format[0]
	}

	// Named formats. Any other value is used as a Go layout string.
	switch f {
	case "human":
		f = humanDateFormat
	case "short":
		f = shortDateFormat
	case "date":
		f = "2006-01-02"
	case "time":
		f = "15:04:05"
	case "datetime":
		f = defaultDateFormat
	}

	return t.Format(f)
}

func dateFormat(format string, v any) string {
	return formatDate(v, format)
}

func timeAgo(v any) string {
	t, err := parseTime(v)
	if err != nil {
		return ""
	}

	duration := time.Since(t)
	return humanizeDuration(duration) + " ago"
}

func timeFrom(v any) string {
	t, err := parseTime(v)
	if err != nil {
		return ""
	}

	duration := time.Until(t)
	if duration < 0 {
		return humanizeDuration(-duration) + " ago"
	}
	return "in " + humanizeDuration(duration)
}

func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	if d < 30*24*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	if d < 365*24*time.Hour {
		return fmt.Sprintf("%d months", int(d.Hours()/(24*30)))
	}
	return fmt.Sprintf("%d years", int(d.Hours()/(24*365)))
}

func dateAdd(v any, amount int, unit string) (time.Time, error) {
	t, err := parseTime(v)
	if err != nil {
		return time.Time{}, err
	}

	switch unit {
	case "second", "seconds":
		return t.Add(time.Duration(amount) * time.Second), nil
	case "minute", "minutes":
		return t.Add(time.Duration(amount) * time.Minute), nil
	case "hour", "hours":
		return t.Add(time.Duration(amount) * time.Hour), nil
	case "day", "days":
		return t.AddDate(0, 0, amount), nil
	case "week", "weeks":
		return t.AddDate(0, 0, amount*7), nil
	case "month", "months":
		return t.AddDate(0, amount, 0), nil
	case "year", "years":
		return t.AddDate(amount, 0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unknown unit: %s", unit)
	}
}

func dateSub(v any, amount int, unit string) (time.Time, error) {
	return dateAdd(v, -amount, unit)
}

func dateDiff(v1, v2 any, unit string) (int64, error) {
	t1, err := parseTime(v1)
	if err != nil {
		return 0, err
	}

	t2, err := parseTime(v2)
	if err != nil {
		return 0, err
	}

	diff := t1.Sub(t2)

	switch unit {
	case "second", "seconds":
		return int64(diff.Seconds()), nil
	case "minute", "minutes":
		return int64(diff.Minutes()), nil
	case "hour", "hours":
		return int64(diff.Hours()), nil
	case "day", "days":
		return int64(diff.Hours() / 24), nil
	case "week", "weeks":
		return int64(diff.Hours() / (24 * 7)), nil
	default:
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}
}

func dateCompare(v1, v2 any) (int, error) {
	t1, err := parseTime(v1)
	if err != nil {
		return 0, err
	}

	t2, err := parseTime(v2)
	if err != nil {
		return 0, err
	}

	if t1.Before(t2) {
		return -1, nil
	}
	if t1.After(t2) {
		return 1, nil
	}
	return 0, nil
}

func now() time.Time {
	return time.Now()
}

func today() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func tomorrow() time.Time {
	return today().AddDate(0, 0, 1)
}

func yesterday() time.Time {
	return today().AddDate(0, 0, -1)
}

func weekday(v any) string {
	t, err := parseTime(v)
	if err != nil {
		return ""
	}
	return t.Weekday().String()
}

func month(v any) string {
	t, err := parseTime(v)
	if err != nil {
		return ""
	}
	return t.Month().String()
}

func year(v any) int {
	t, err := parseTime(v)
	if err != nil {
		return 0
	}
	return t.Year()
}

func day(v any) int {
	t, err := parseTime(v)
	if err != nil {
		return 0
	}
	return t.Day()
}

func hour(v any) int {
	t, err := parseTime(v)
	if err != nil {
		return 0
	}
	return t.Hour()
}

func minute(v any) int {
	t, err := parseTime(v)
	if err != nil {
		return 0
	}
	return t.Minute()
}

func second(v any) int {
	t, err := parseTime(v)
	if err != nil {
		return 0
	}
	return t.Second()
}

func unix(v any) int64 {
	t, err := parseTime(v)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func fromUnix(timestamp int64) time.Time {
	return time.Unix(timestamp, 0)
}

func iso8601(v any) string {
	t, err := parseTime(v)
	if err != nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func rfc3339(v any) string {
	t, err := parseTime(v)
	if err != nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func humanDuration(seconds any) string {
	var s int64
	switch v := seconds.(type) {
	case int:
		s = int64(v)
	case int64:
		s = v
	case float64:
		s = int64(v)
	default:
		return ""
	}

	d := time.Duration(s) * time.Second
	return humanizeDuration(d)
}

func durationSeconds(d time.Duration) float64 {
	return d.Seconds()
}

func timezone(v any, tz string) (time.Time, error) {
	t, err := parseTime(v)
	if err != nil {
		return time.Time{}, err
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, err
	}

	return t.In(loc), nil
}
