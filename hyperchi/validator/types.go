package validator

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// StringValidator checks a string's length, format, and allowed values, after
// applying any transforms (Trim, ToLower, ToUpper) in the order they were
// added. Its methods configure it in place and return it for chaining.
//
// Unless Required is set, an empty or whitespace-only string passes without
// any other check: an HTML form submits an untouched optional input as "".
type StringValidator struct {
	BaseValidator
	minLength  *int
	maxLength  *int
	patterns   []*regexp.Regexp
	oneOf      []string
	transforms []func(string) string
}

// String returns a StringValidator with no rules.
func String() *StringValidator {
	return &StringValidator{
		BaseValidator: BaseValidator{},
	}
}

// MinLength sets the minimum string length, counted in runes (characters),
// not bytes.
func (s *StringValidator) MinLength(min int) *StringValidator {
	s.minLength = &min
	return s
}

// MaxLength sets the maximum string length, counted in runes (characters),
// not bytes, so "café" is 4 long.
func (s *StringValidator) MaxLength(max int) *StringValidator {
	s.maxLength = &max
	return s
}

// Length sets the exact string length, counted in runes.
func (s *StringValidator) Length(length int) *StringValidator {
	s.minLength = &length
	s.maxLength = &length
	return s
}

// Pattern requires the string to match the regular expression pattern. It
// panics if pattern does not compile. Pattern, Regex, Email, Phone, URL, and
// UUID each add a rule, and the string must match every one.
func (s *StringValidator) Pattern(pattern string) *StringValidator {
	return s.Regex(regexp.MustCompile(pattern))
}

// Regex requires the string to match pattern, in addition to any earlier
// pattern rule. A nil pattern adds no rule.
func (s *StringValidator) Regex(pattern *regexp.Regexp) *StringValidator {
	if pattern != nil {
		s.patterns = append(s.patterns, pattern)
	}
	return s
}

// OneOf requires the string to equal one of values, compared case-sensitively
// after transforms.
func (s *StringValidator) OneOf(values ...string) *StringValidator {
	s.oneOf = values
	return s
}

// Email requires the string to match EmailRegex, in addition to any
// earlier pattern rule.
func (s *StringValidator) Email() *StringValidator {
	return s.Regex(EmailRegex)
}

// Phone requires the string to match PhoneRegex, in addition to any
// earlier pattern rule.
func (s *StringValidator) Phone() *StringValidator {
	return s.Regex(PhoneRegex)
}

// URL requires the string to match URLRegex (http or https), in addition to any
// earlier pattern rule.
func (s *StringValidator) URL() *StringValidator {
	return s.Regex(URLRegex)
}

// UUID requires the string to match UUIDRegex (lowercase hex), in addition to any
// earlier pattern rule.
func (s *StringValidator) UUID() *StringValidator {
	return s.Regex(UUIDRegex)
}

// Trim adds a transform that removes leading and trailing whitespace.
func (s *StringValidator) Trim() *StringValidator {
	s.transforms = append(s.transforms, strings.TrimSpace)
	return s
}

// ToLower adds a transform that lowercases the string.
func (s *StringValidator) ToLower() *StringValidator {
	s.transforms = append(s.transforms, strings.ToLower)
	return s
}

// ToUpper adds a transform that uppercases the string.
func (s *StringValidator) ToUpper() *StringValidator {
	s.transforms = append(s.transforms, strings.ToUpper)
	return s
}

// Validate applies the transforms, then checks length, patterns, and OneOf in
// that order, returning the first failure. A non-string value fails. An
// optional field's empty or whitespace-only string passes.
func (s *StringValidator) Validate(value any) error {
	str, ok := value.(string)
	if !ok {
		return s.NewValidationError("value must be a string")
	}

	for _, transform := range s.transforms {
		str = transform(str)
	}

	if !s.IsRequired() && strings.TrimSpace(str) == "" {
		return nil
	}

	// Lengths count characters, not bytes.
	length := utf8.RuneCountInString(str)
	if s.minLength != nil && length < *s.minLength {
		return s.NewValidationError(fmt.Sprintf("string must be at least %d characters", *s.minLength))
	}

	if s.maxLength != nil && length > *s.maxLength {
		return s.NewValidationError(fmt.Sprintf("string must be at most %d characters", *s.maxLength))
	}

	for _, pattern := range s.patterns {
		if !pattern.MatchString(str) {
			return s.NewValidationError("string format is invalid")
		}
	}

	if len(s.oneOf) > 0 {
		for _, allowed := range s.oneOf {
			if str == allowed {
				return nil
			}
		}
		return s.NewValidationError(fmt.Sprintf("value must be one of: %s", strings.Join(s.oneOf, ", ")))
	}

	return nil
}

// Transform returns a string value with the transforms applied. Other values
// come back unchanged.
func (s *StringValidator) Transform(value any) any {
	if str, ok := value.(string); ok && len(s.transforms) > 0 {
		for _, transform := range s.transforms {
			str = transform(str)
		}
		return str
	}
	return value
}

// Required marks the field as required. Schema.Validate treats a blank or
// whitespace-only string as missing.
func (s *StringValidator) Required() *StringValidator {
	s.BaseValidator.Required()
	return s
}

// Default sets the value used when the field is absent.
func (s *StringValidator) Default(value string) *StringValidator {
	s.BaseValidator.Default(value)
	return s
}

// Message replaces every failure message of this validator with msg.
func (s *StringValidator) Message(msg string) *StringValidator {
	s.BaseValidator.Message(msg)
	return s
}

// NumberValidator checks a number's range, sign, integrality, and step. It
// accepts the types and strings ParseNumber does. Its methods configure it in
// place and return it for chaining.
type NumberValidator struct {
	BaseValidator
	min        *float64
	max        *float64
	integer    bool
	positive   bool
	negative   bool
	multipleOf *float64
}

// Number returns a NumberValidator with no rules.
func Number() *NumberValidator {
	return &NumberValidator{
		BaseValidator: BaseValidator{},
	}
}

// Min sets the inclusive minimum.
func (n *NumberValidator) Min(min float64) *NumberValidator {
	n.min = &min
	return n
}

// Max sets the inclusive maximum.
func (n *NumberValidator) Max(max float64) *NumberValidator {
	n.max = &max
	return n
}

// Range sets the inclusive minimum and maximum.
func (n *NumberValidator) Range(min, max float64) *NumberValidator {
	n.min = &min
	n.max = &max
	return n
}

// Integer requires a whole number. Transform then returns an int.
func (n *NumberValidator) Integer() *NumberValidator {
	n.integer = true
	return n
}

// Positive requires a number greater than zero.
func (n *NumberValidator) Positive() *NumberValidator {
	n.positive = true
	return n
}

// Negative requires a number less than zero.
func (n *NumberValidator) Negative() *NumberValidator {
	n.negative = true
	return n
}

// MultipleOf requires a multiple of value. Decimal
// steps such as 0.1 are accepted despite binary rounding (0.3 is a multiple
// of 0.1). value must be finite and non-zero; MultipleOf panics otherwise,
// since that is a programming error in the schema.
func (n *NumberValidator) MultipleOf(value float64) *NumberValidator {
	if value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		panic(fmt.Sprintf("validator: MultipleOf(%g) needs a finite, non-zero step", value))
	}
	n.multipleOf = &value
	return n
}

// isMultipleOf reports whether num is an integer multiple of step, allowing
// for the rounding error of the two float64 values. math.Remainder is exact,
// so the only error is in how num and step were represented: about one ulp of
// num plus the quotient times one ulp of step.
func isMultipleOf(num, step float64) bool {
	remainder := math.Abs(math.Remainder(num, step))
	quotient := math.Abs(math.Round(num / step))
	tolerance := 1e-9*math.Abs(step) + ulp(num) + quotient*ulp(step)
	return remainder <= tolerance
}

// ulp returns the gap between |x| and the next larger float64.
func ulp(x float64) float64 {
	x = math.Abs(x)
	return math.Nextafter(x, math.Inf(1)) - x
}

// Validate parses value with ParseNumber, rejects NaN and infinities, then
// checks the range, Integer, sign, and step rules, returning the first
// failure.
func (n *NumberValidator) Validate(value any) error {
	num, err := ParseNumber(value)
	if err != nil {
		return n.NewValidationError("value must be a number")
	}
	// ParseFloat accepts "NaN" and "Inf". NaN compares false against every
	// bound, so it would pass Min and Max; neither is a usable number.
	if math.IsNaN(num) || math.IsInf(num, 0) {
		return n.NewValidationError("value must be a finite number")
	}

	if n.min != nil && num < *n.min {
		return n.NewValidationError(fmt.Sprintf("number must be at least %g", *n.min))
	}

	if n.max != nil && num > *n.max {
		return n.NewValidationError(fmt.Sprintf("number must be at most %g", *n.max))
	}

	if n.integer && num != math.Trunc(num) {
		return n.NewValidationError("value must be an integer")
	}

	if n.positive && num <= 0 {
		return n.NewValidationError("number must be positive")
	}

	if n.negative && num >= 0 {
		return n.NewValidationError("number must be negative")
	}

	if n.multipleOf != nil && !isMultipleOf(num, *n.multipleOf) {
		return n.NewValidationError(fmt.Sprintf("number must be a multiple of %g", *n.multipleOf))
	}

	return nil
}

// Transform returns the parsed number as a float64, or as an int (truncated)
// when Integer is set. A value that does not parse comes back unchanged.
func (n *NumberValidator) Transform(value any) any {
	if num, err := ParseNumber(value); err == nil {
		if n.integer {
			return int(num)
		}
		return num
	}
	return value
}

// Required marks the field as required. Zero is a present value.
func (n *NumberValidator) Required() *NumberValidator {
	n.BaseValidator.Required()
	return n
}

// Default sets the value used when the field is absent.
func (n *NumberValidator) Default(value float64) *NumberValidator {
	n.BaseValidator.Default(value)
	return n
}

// Message replaces every failure message of this validator with msg.
func (n *NumberValidator) Message(msg string) *NumberValidator {
	n.BaseValidator.Message(msg)
	return n
}

// BooleanValidator accepts any value ParseBoolean can read.
type BooleanValidator struct {
	BaseValidator
}

// Boolean returns a BooleanValidator.
func Boolean() *BooleanValidator {
	return &BooleanValidator{
		BaseValidator: BaseValidator{},
	}
}

// Validate fails when ParseBoolean cannot read value.
func (b *BooleanValidator) Validate(value any) error {
	_, err := ParseBoolean(value)
	if err != nil {
		return b.NewValidationError("value must be a boolean")
	}
	return nil
}

// Transform returns the parsed bool, or value unchanged when it does not
// parse.
func (b *BooleanValidator) Transform(value any) any {
	if val, err := ParseBoolean(value); err == nil {
		return val
	}
	return value
}

// Required marks the field as required. False is a present value, so this
// does not mean "must be checked". An unchecked HTML checkbox is absent
// and fails.
func (b *BooleanValidator) Required() *BooleanValidator {
	b.BaseValidator.Required()
	return b
}

// Default sets the value used when the field is absent.
func (b *BooleanValidator) Default(value bool) *BooleanValidator {
	b.BaseValidator.Default(value)
	return b
}

// Message replaces every failure message of this validator with msg.
func (b *BooleanValidator) Message(msg string) *BooleanValidator {
	b.BaseValidator.Message(msg)
	return b
}

// DateValidator checks a date parsed with ParseTime, or with the layout set by
// Format, against optional bounds. Its methods configure it in place and
// return it for chaining.
type DateValidator struct {
	BaseValidator
	after  *time.Time
	before *time.Time
	format string
}

// Date returns a DateValidator with no rules.
func Date() *DateValidator {
	return &DateValidator{
		BaseValidator: BaseValidator{},
	}
}

// After sets the earliest allowed date. The bound itself passes.
func (d *DateValidator) After(date time.Time) *DateValidator {
	d.after = &date
	return d
}

// Before sets the latest allowed date. The bound itself passes.
func (d *DateValidator) Before(date time.Time) *DateValidator {
	d.before = &date
	return d
}

// Format sets the expected date layout for string parsing, replacing the ISO
// 8601 layouts ParseTime accepts. Use it to opt in to a slash format, which
// ParseTime rejects as ambiguous: Format("01/02/2006") for US month-first
// dates, Format("02/01/2006") for day-first dates.
func (d *DateValidator) Format(format string) *DateValidator {
	d.format = format
	return d
}

// Validate parses value and checks the After and Before bounds. With Format
// set, value must be a string in that layout.
func (d *DateValidator) Validate(value any) error {
	var date time.Time
	var err error

	if d.format != "" {
		if str, ok := value.(string); ok {
			date, err = time.Parse(d.format, str)
		} else {
			return d.NewValidationError("date must be a string when format is specified")
		}
	} else {
		date, err = ParseTime(value)
	}

	if err != nil {
		return d.NewValidationError("invalid date format")
	}

	if d.after != nil && date.Before(*d.after) {
		return d.NewValidationError(fmt.Sprintf("date must be after %s", d.after.Format("2006-01-02")))
	}

	if d.before != nil && date.After(*d.before) {
		return d.NewValidationError(fmt.Sprintf("date must be before %s", d.before.Format("2006-01-02")))
	}

	return nil
}

// Transform returns the parsed time.Time, or value unchanged when it does not
// parse.
func (d *DateValidator) Transform(value any) any {
	if d.format != "" {
		if str, ok := value.(string); ok {
			if date, err := time.Parse(d.format, str); err == nil {
				return date
			}
		}
	} else {
		if date, err := ParseTime(value); err == nil {
			return date
		}
	}
	return value
}

// Required marks the field as required.
func (d *DateValidator) Required() *DateValidator {
	d.BaseValidator.Required()
	return d
}

// Default sets the value used when the field is absent.
func (d *DateValidator) Default(value time.Time) *DateValidator {
	d.BaseValidator.Default(value)
	return d
}

// Message replaces every failure message of this validator with msg.
func (d *DateValidator) Message(msg string) *DateValidator {
	d.BaseValidator.Message(msg)
	return d
}

// ArrayValidator checks a []string or []any: its length, uniqueness, and each
// item against an item validator. Its methods configure it in place and
// return it for chaining.
type ArrayValidator struct {
	BaseValidator
	minItems      *int
	maxItems      *int
	itemValidator FieldValidator
	unique        bool
}

// Array returns an ArrayValidator that checks each item with itemValidator,
// which may be nil to skip item checks.
func Array(itemValidator FieldValidator) *ArrayValidator {
	return &ArrayValidator{
		BaseValidator: BaseValidator{},
		itemValidator: itemValidator,
	}
}

// MinItems sets the minimum number of items.
func (a *ArrayValidator) MinItems(min int) *ArrayValidator {
	a.minItems = &min
	return a
}

// MaxItems sets the maximum number of items.
func (a *ArrayValidator) MaxItems(max int) *ArrayValidator {
	a.maxItems = &max
	return a
}

// Length sets the exact number of items.
func (a *ArrayValidator) Length(length int) *ArrayValidator {
	a.minItems = &length
	a.maxItems = &length
	return a
}

// Unique rejects duplicate items. Comparable items are compared with ==, and
// others, such as maps and slices, with reflect.DeepEqual.
func (a *ArrayValidator) Unique() *ArrayValidator {
	a.unique = true
	return a
}

// Validate checks a []string or []any. Any other type fails, including other
// slice types such as []int.
func (a *ArrayValidator) Validate(value any) error {
	if strSlice, ok := value.([]string); ok {
		items := make([]any, len(strSlice))
		for i, v := range strSlice {
			items[i] = v
		}
		return a.validateItems(items)
	}

	if slice, ok := value.([]any); ok {
		return a.validateItems(slice)
	}

	return a.NewValidationError("value must be an array")
}

func (a *ArrayValidator) validateItems(items []any) error {
	if a.minItems != nil && len(items) < *a.minItems {
		return a.NewValidationError(fmt.Sprintf("array must have at least %d items", *a.minItems))
	}

	if a.maxItems != nil && len(items) > *a.maxItems {
		return a.NewValidationError(fmt.Sprintf("array must have at most %d items", *a.maxItems))
	}

	if a.unique {
		seen := make(map[any]bool)
		var compound []any
		for _, item := range items {
			if item != nil && !reflect.ValueOf(item).Comparable() {
				for _, previous := range compound {
					if reflect.DeepEqual(item, previous) {
						return a.NewValidationError("array items must be unique")
					}
				}
				compound = append(compound, item)
				continue
			}
			if seen[item] {
				return a.NewValidationError("array items must be unique")
			}
			seen[item] = true
		}
	}

	if a.itemValidator != nil {
		for i, item := range items {
			if err := a.itemValidator.Validate(item); err != nil {
				return a.NewValidationError(fmt.Sprintf("item %d: %s", i, err.Error()))
			}
		}
	}

	return nil
}

// Transform runs the item validator's Transform over a []any. A []string, or
// an array without an item validator, comes back unchanged.
func (a *ArrayValidator) Transform(value any) any {
	if items, ok := value.([]any); ok && a.itemValidator != nil {
		transformed := make([]any, len(items))
		for i, item := range items {
			transformed[i] = a.itemValidator.Transform(item)
		}
		return transformed
	}
	return value
}

// Required marks the field as required.
func (a *ArrayValidator) Required() *ArrayValidator {
	a.BaseValidator.Required()
	return a
}

// Default sets the value used when the field is absent.
func (a *ArrayValidator) Default(value []any) *ArrayValidator {
	a.BaseValidator.Default(value)
	return a
}

// Message replaces every failure message of this validator with msg.
func (a *ArrayValidator) Message(msg string) *ArrayValidator {
	a.BaseValidator.Message(msg)
	return a
}

// Int returns Number().Integer().
func Int() *NumberValidator {
	return Number().Integer()
}

// Float returns Number().
func Float() *NumberValidator {
	return Number()
}

// Bool returns Boolean().
func Bool() *BooleanValidator {
	return Boolean()
}

// DateTime returns Date().
func DateTime() *DateValidator {
	return Date()
}

// Email returns String().Email().
func Email() *StringValidator {
	return String().Email()
}

// URL returns String().URL().
func URL() *StringValidator {
	return String().URL()
}
