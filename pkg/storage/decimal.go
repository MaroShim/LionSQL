package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var (
	ErrDivisionByZero = errors.New("division by zero")
	ErrInvalidDecimal = errors.New("invalid decimal format")
)

var bigTen = big.NewInt(10)

// Decimal represents a fixed-point numeric value with arbitrary precision and exact decimal math.
// Value is mathematically: value * 10^(-scale).
type Decimal struct {
	value *big.Int
	scale int32
}

// ZeroDecimal returns a Decimal initialized to 0 with the specified scale.
func ZeroDecimal(scale int32) Decimal {
	if scale < 0 {
		scale = 0
	}
	return Decimal{
		value: big.NewInt(0),
		scale: scale,
	}
}

// NewDecimal creates a Decimal from an unscaled big.Int and a scale.
func NewDecimal(unscaled *big.Int, scale int32) Decimal {
	if unscaled == nil {
		unscaled = big.NewInt(0)
	}
	if scale < 0 {
		scale = 0
	}
	return Decimal{
		value: new(big.Int).Set(unscaled),
		scale: scale,
	}
}

// NewDecimalFromInt creates a Decimal from an int64 with given scale.
func NewDecimalFromInt(n int64, scale int32) Decimal {
	d := Decimal{
		value: big.NewInt(n),
		scale: 0,
	}
	if scale > 0 {
		return d.Rescale(scale)
	}
	return d
}

// NewDecimalFromFloat creates a Decimal from float64 with requested scale.
func NewDecimalFromFloat(f float64, scale int32) Decimal {
	if scale < 0 {
		scale = 2
	}
	format := fmt.Sprintf("%%.%df", scale)
	str := fmt.Sprintf(format, f)
	dec, err := ParseDecimal(str)
	if err != nil {
		return ZeroDecimal(scale)
	}
	return dec
}

// ParseDecimal parses a string into an exact Decimal.
// Supports integers ("123"), decimals ("123.45", "-0.0500"), and scientific notation ("1e-2").
func ParseDecimal(s string) (Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ZeroDecimal(0), ErrInvalidDecimal
	}

	// Handle scientific notation via big.Rat for exact parsing
	if strings.ContainsAny(s, "eE") {
		rat, ok := new(big.Rat).SetString(s)
		if !ok {
			return ZeroDecimal(0), fmt.Errorf("%w: %s", ErrInvalidDecimal, s)
		}
		// Convert big.Rat to decimal string with 18 decimal places default
		fStr := rat.FloatString(18)
		fStr = strings.TrimRight(strings.TrimRight(fStr, "0"), ".")
		if fStr == "" || fStr == "-" {
			fStr = "0"
		}
		s = fStr
	}

	negative := false
	if s[0] == '+' {
		s = s[1:]
	} else if s[0] == '-' {
		negative = true
		s = s[1:]
	}

	if s == "" {
		return ZeroDecimal(0), ErrInvalidDecimal
	}

	dotIdx := strings.IndexByte(s, '.')
	var unscaledStr string
	var scale int32

	if dotIdx == -1 {
		unscaledStr = s
		scale = 0
	} else {
		integerPart := s[:dotIdx]
		fractionalPart := s[dotIdx+1:]

		// Handle trailing dot: "123."
		if fractionalPart == "" {
			unscaledStr = integerPart
			scale = 0
		} else {
			// Handle leading dot: ".45"
			if integerPart == "" {
				integerPart = "0"
			}
			unscaledStr = integerPart + fractionalPart
			scale = int32(len(fractionalPart))
		}
	}

	unscaled := new(big.Int)
	if _, ok := unscaled.SetString(unscaledStr, 10); !ok {
		return ZeroDecimal(0), fmt.Errorf("%w: %s", ErrInvalidDecimal, s)
	}

	if negative {
		unscaled.Neg(unscaled)
	}

	return Decimal{
		value: unscaled,
		scale: scale,
	}, nil
}

// IsZero reports whether d is 0.
func (d Decimal) IsZero() bool {
	return d.value == nil || d.value.Sign() == 0
}

// Scale returns the scale of the Decimal.
func (d Decimal) Scale() int32 {
	return d.scale
}

// UnscaledValue returns a copy of the unscaled big.Int.
func (d Decimal) UnscaledValue() *big.Int {
	if d.value == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(d.value)
}

// String returns the canonical decimal representation of d.
func (d Decimal) String() string {
	if d.value == nil {
		if d.scale <= 0 {
			return "0"
		}
		return "0." + strings.Repeat("0", int(d.scale))
	}

	isNeg := d.value.Sign() < 0
	absVal := new(big.Int).Abs(d.value)
	absStr := absVal.String()

	if d.scale <= 0 {
		if isNeg {
			return "-" + absStr
		}
		return absStr
	}

	scaleInt := int(d.scale)
	var res strings.Builder
	if isNeg {
		res.WriteByte('-')
	}

	if len(absStr) <= scaleInt {
		res.WriteString("0.")
		res.WriteString(strings.Repeat("0", scaleInt-len(absStr)))
		res.WriteString(absStr)
	} else {
		splitIdx := len(absStr) - scaleInt
		res.WriteString(absStr[:splitIdx])
		res.WriteByte('.')
		res.WriteString(absStr[splitIdx:])
	}

	return res.String()
}

// Cmp compares d and other mathematically.
// Returns -1 if d < other, 0 if d == other, 1 if d > other.
func (d Decimal) Cmp(other Decimal) int {
	v1, v2, _ := alignScales(d, other)
	return v1.Cmp(v2)
}

// Equal reports whether d and other represent the exact same numeric value.
func (d Decimal) Equal(other Decimal) bool {
	return d.Cmp(other) == 0
}

// Add returns d + other.
func (d Decimal) Add(other Decimal) Decimal {
	v1, v2, commonScale := alignScales(d, other)
	sum := new(big.Int).Add(v1, v2)
	return Decimal{
		value: sum,
		scale: commonScale,
	}
}

// Sub returns d - other.
func (d Decimal) Sub(other Decimal) Decimal {
	v1, v2, commonScale := alignScales(d, other)
	diff := new(big.Int).Sub(v1, v2)
	return Decimal{
		value: diff,
		scale: commonScale,
	}
}

// Mul returns d * other.
func (d Decimal) Mul(other Decimal) Decimal {
	val1 := d.UnscaledValue()
	val2 := other.UnscaledValue()
	prod := new(big.Int).Mul(val1, val2)
	return Decimal{
		value: prod,
		scale: d.scale + other.scale,
	}
}

// Div returns d / other rescaled to targetScale with standard half-up rounding.
func (d Decimal) Div(other Decimal, targetScale int32) (Decimal, error) {
	if other.IsZero() {
		return ZeroDecimal(targetScale), ErrDivisionByZero
	}
	if targetScale < 0 {
		targetScale = 4
	}

	// Result = (d.value / other.value) * 10^(targetScale - (d.scale - other.scale))
	shift := targetScale + other.scale - d.scale
	val1 := d.UnscaledValue()
	val2 := other.UnscaledValue()

	if shift > 0 {
		exp := exp10(shift)
		val1.Mul(val1, exp)
	} else if shift < 0 {
		exp := exp10(-shift)
		val2.Mul(val2, exp)
	}

	// Perform division with remainder for half-up rounding
	quo := new(big.Int)
	rem := new(big.Int)
	quo.QuoRem(val1, val2, rem)

	if rem.Sign() != 0 {
		// double the remainder and compare with divisor for half-up rounding
		twoRem := new(big.Int).Abs(rem)
		twoRem.Lsh(twoRem, 1) // rem * 2
		absDivisor := new(big.Int).Abs(val2)

		if twoRem.Cmp(absDivisor) >= 0 {
			if (val1.Sign() < 0) == (val2.Sign() < 0) {
				quo.Add(quo, big.NewInt(1))
			} else {
				quo.Sub(quo, big.NewInt(1))
			}
		}
	}

	return Decimal{
		value: quo,
		scale: targetScale,
	}, nil
}

// Rescale adjusts the scale of d to targetScale using standard half-up rounding.
func (d Decimal) Rescale(targetScale int32) Decimal {
	if targetScale < 0 {
		targetScale = 0
	}
	if d.scale == targetScale {
		return Decimal{
			value: d.UnscaledValue(),
			scale: targetScale,
		}
	}

	val := d.UnscaledValue()
	if targetScale > d.scale {
		diff := targetScale - d.scale
		val.Mul(val, exp10(diff))
		return Decimal{
			value: val,
			scale: targetScale,
		}
	}

	// targetScale < d.scale: integer division with half-up rounding
	diff := d.scale - targetScale
	divisor := exp10(diff)

	quo := new(big.Int)
	rem := new(big.Int)
	quo.QuoRem(val, divisor, rem)

	if rem.Sign() != 0 {
		twoRem := new(big.Int).Abs(rem)
		twoRem.Lsh(twoRem, 1)
		if twoRem.Cmp(divisor) >= 0 {
			if val.Sign() >= 0 {
				quo.Add(quo, big.NewInt(1))
			} else {
				quo.Sub(quo, big.NewInt(1))
			}
		}
	}

	return Decimal{
		value: quo,
		scale: targetScale,
	}
}

// Float64 converts Decimal to float64 (approximate).
func (d Decimal) Float64() float64 {
	if d.value == nil || d.value.Sign() == 0 {
		return 0.0
	}
	rat := d.toRat()
	f, _ := rat.Float64()
	return f
}

// Int64 converts Decimal to int64 by truncating fractional digits.
func (d Decimal) Int64() int64 {
	rescaled := d.Rescale(0)
	if rescaled.value == nil {
		return 0
	}
	return rescaled.value.Int64()
}

func (d Decimal) toRat() *big.Rat {
	val := d.UnscaledValue()
	denom := exp10(d.scale)
	return new(big.Rat).SetFrac(val, denom)
}

func alignScales(a, b Decimal) (*big.Int, *big.Int, int32) {
	v1 := a.UnscaledValue()
	v2 := b.UnscaledValue()

	if a.scale == b.scale {
		return v1, v2, a.scale
	}

	if a.scale < b.scale {
		diff := b.scale - a.scale
		v1.Mul(v1, exp10(diff))
		return v1, v2, b.scale
	}

	diff := a.scale - b.scale
	v2.Mul(v2, exp10(diff))
	return v1, v2, a.scale
}

func exp10(n int32) *big.Int {
	if n <= 0 {
		return big.NewInt(1)
	}
	res := new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
	return res
}

// MarshalJSON serializes Decimal to a JSON string representation.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON deserializes Decimal from a JSON string or number representation.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		dec, pErr := ParseDecimal(s)
		if pErr != nil {
			*d = ZeroDecimal(2)
			return nil
		}
		*d = dec
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*d = NewDecimalFromFloat(f, 4)
		return nil
	}
	*d = ZeroDecimal(2)
	return nil
}
