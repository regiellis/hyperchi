package filters

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func registerNumberFilters() {
	Register("number", formatNumber)
	Register("decimal", formatDecimal)
	Register("currency", formatCurrency)
	Register("percent", formatPercent)
	Register("ordinal", ordinal)
	Register("bytes", formatBytes)
	Register("round", round)
	Register("ceil", ceil)
	Register("floor", floor)
	Register("abs", abs)
	Register("min", minOf)
	Register("max", maxOf)
	Register("sum", sum)
	Register("avg", avg)
	Register("add", add)
	Register("sub", sub)
	Register("mul", mul)
	Register("div", div)
	Register("mod", mod)
	Register("pow", pow)
	Register("sqrt", sqrt)
	Register("humanize", humanizeNumber)
	Register("commas", addCommas)
	Register("padNumber", padNumber)
	Register("roman", toRoman)
}

func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case uint:
		return float64(n), nil
	case uint32:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(n, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

func formatNumber(v any, precision ...int) string {
	n, err := toFloat64(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	p := 0
	if len(precision) > 0 {
		p = precision[0]
	}

	return fmt.Sprintf("%.*f", p, n)
}

func formatDecimal(v any, decimals int) string {
	n, err := toFloat64(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	format := fmt.Sprintf("%%.%df", decimals)
	return fmt.Sprintf(format, n)
}

func formatCurrency(v any, symbol ...string) string {
	n, err := toFloat64(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	sym := "$"
	if len(symbol) > 0 {
		sym = symbol[0]
	}

	formatted := addCommas(fmt.Sprintf("%.2f", n))

	// Put the minus sign before the currency symbol: -$5.00, not $-5.00.
	if n < 0 {
		return fmt.Sprintf("-%s%s", sym, formatted[1:])
	}

	return sym + formatted
}

func formatPercent(v any, decimals ...int) string {
	n, err := toFloat64(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	d := 0
	if len(decimals) > 0 {
		d = decimals[0]
	}

	return fmt.Sprintf("%.*f%%", d, n*100)
}

func ordinal(v any) string {
	n, err := toFloat64(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	i := int(n)
	suffix := "th"

	if i%100 < 11 || i%100 > 13 {
		switch i % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}

	return fmt.Sprintf("%d%s", i, suffix)
}

func formatBytes(v any) string {
	n, err := toFloat64(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}

	if n < 0 {
		return "-" + formatBytes(-n)
	}

	if n < 1 {
		return "0 B"
	}

	exponent := int(math.Log(n) / math.Log(1024))
	if exponent >= len(units) {
		exponent = len(units) - 1
	}

	divisor := math.Pow(1024, float64(exponent))
	value := n / divisor

	if value >= 100 {
		return fmt.Sprintf("%.0f %s", value, units[exponent])
	} else if value >= 10 {
		return fmt.Sprintf("%.1f %s", value, units[exponent])
	}
	return fmt.Sprintf("%.2f %s", value, units[exponent])
}

func round(v any, precision ...int) float64 {
	n, err := toFloat64(v)
	if err != nil {
		return 0
	}

	p := 0
	if len(precision) > 0 {
		p = precision[0]
	}

	shift := math.Pow(10, float64(p))
	return math.Round(n*shift) / shift
}

func ceil(v any) float64 {
	n, err := toFloat64(v)
	if err != nil {
		return 0
	}
	return math.Ceil(n)
}

func floor(v any) float64 {
	n, err := toFloat64(v)
	if err != nil {
		return 0
	}
	return math.Floor(n)
}

func abs(v any) float64 {
	n, err := toFloat64(v)
	if err != nil {
		return 0
	}
	return math.Abs(n)
}

// minOf returns the smallest numeric value, skipping values that are not
// numbers (as sum does), or 0 when none is numeric.
func minOf(values ...any) float64 {
	return extremum(values, func(v, best float64) bool { return v < best })
}

// maxOf returns the largest numeric value, skipping values that are not
// numbers, or 0 when none is numeric.
func maxOf(values ...any) float64 {
	return extremum(values, func(v, best float64) bool { return v > best })
}

// extremum returns the numeric value for which better(v, best) beats every
// other, or 0 when no value converts to a number.
func extremum(values []any, better func(v, best float64) bool) float64 {
	best, found := 0.0, false
	for _, v := range values {
		n, err := toFloat64(v)
		if err != nil {
			continue
		}
		if !found || better(n, best) {
			best, found = n, true
		}
	}
	return best
}

func sum(values ...any) float64 {
	total := 0.0

	for _, v := range values {
		if val, err := toFloat64(v); err == nil {
			total += val
		}
	}

	return total
}

// avg averages the numeric values, skipping the rest as sum does.
func avg(values ...any) float64 {
	total, count := 0.0, 0
	for _, v := range values {
		if val, err := toFloat64(v); err == nil {
			total += val
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func add(a, b any) float64 {
	aVal, _ := toFloat64(a)
	bVal, _ := toFloat64(b)
	return aVal + bVal
}

func sub(a, b any) float64 {
	aVal, _ := toFloat64(a)
	bVal, _ := toFloat64(b)
	return aVal - bVal
}

func mul(a, b any) float64 {
	aVal, _ := toFloat64(a)
	bVal, _ := toFloat64(b)
	return aVal * bVal
}

func div(a, b any) float64 {
	aVal, _ := toFloat64(a)
	bVal, _ := toFloat64(b)

	if bVal == 0 {
		return 0
	}

	return aVal / bVal
}

func mod(a, b any) float64 {
	aVal, _ := toFloat64(a)
	bVal, _ := toFloat64(b)

	if bVal == 0 {
		return 0
	}

	return math.Mod(aVal, bVal)
}

func pow(base, exp any) float64 {
	baseVal, _ := toFloat64(base)
	expVal, _ := toFloat64(exp)
	return math.Pow(baseVal, expVal)
}

func sqrt(v any) float64 {
	n, err := toFloat64(v)
	if err != nil || n < 0 {
		return 0
	}
	return math.Sqrt(n)
}

func humanizeNumber(v any) string {
	n, err := toFloat64(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	if n < 0 {
		return "-" + humanizeNumber(-n)
	}

	suffixes := []string{"", "K", "M", "B", "T"}

	for i := len(suffixes) - 1; i >= 0; i-- {
		divisor := math.Pow(1000, float64(i))
		if n >= divisor {
			value := n / divisor
			if value >= 100 {
				return fmt.Sprintf("%.0f%s", value, suffixes[i])
			} else if value >= 10 {
				return fmt.Sprintf("%.1f%s", value, suffixes[i])
			}
			return fmt.Sprintf("%.2f%s", value, suffixes[i])
		}
	}

	return fmt.Sprintf("%.0f", n)
}

// addCommas inserts thousands separators into the integer part of a
// formatted number: "-1234567.50" becomes "-1,234,567.50".
func addCommas(s string) string {
	parts := strings.Split(s, ".")
	intPart := parts[0]

	negative := false
	if strings.HasPrefix(intPart, "-") {
		negative = true
		intPart = intPart[1:]
	}

	result := ""
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			result += ","
		}
		result += string(r)
	}

	if negative {
		result = "-" + result
	}

	if len(parts) > 1 {
		result += "." + parts[1]
	}

	return result
}

func padNumber(v any, width int, padChar ...string) string {
	s := fmt.Sprint(v)

	pad := "0"
	if len(padChar) > 0 {
		pad = padChar[0]
	}

	if len(s) >= width {
		return s
	}

	return strings.Repeat(pad, width-len(s)) + s
}

var romanNumerals = []struct {
	value  int
	symbol string
}{
	{1000, "M"},
	{900, "CM"},
	{500, "D"},
	{400, "CD"},
	{100, "C"},
	{90, "XC"},
	{50, "L"},
	{40, "XL"},
	{10, "X"},
	{9, "IX"},
	{5, "V"},
	{4, "IV"},
	{1, "I"},
}

func toRoman(v any) string {
	n, err := toFloat64(v)
	if err != nil || n <= 0 || n >= 4000 {
		return fmt.Sprint(v)
	}

	num := int(n)
	result := ""

	for _, rn := range romanNumerals {
		for num >= rn.value {
			result += rn.symbol
			num -= rn.value
		}
	}

	return result
}
