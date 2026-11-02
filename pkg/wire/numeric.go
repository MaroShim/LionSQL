package wire

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"lionsql/pkg/storage"
)

const (
	numericPos = 0x0000
	numericNeg = 0x4000
	numericNan = 0xC000
)

// decodePgBinaryNumeric decodes a PostgreSQL binary NUMERIC (OID 1700) payload into a storage.Decimal.
// Wire format:
//   int16 ndigits
//   int16 weight
//   uint16 sign (0x0000 = pos, 0x4000 = neg, 0xC000 = nan)
//   uint16 dscale
//   [ndigits]int16 digits (base-10000)
func decodePgBinaryNumeric(b []byte) (storage.Decimal, error) {
	if len(b) < 8 {
		return storage.ZeroDecimal(0), fmt.Errorf("numeric payload too short: %d bytes", len(b))
	}
	ndigits := int16(binary.BigEndian.Uint16(b[0:2]))
	weight := int16(binary.BigEndian.Uint16(b[2:4]))
	sign := binary.BigEndian.Uint16(b[4:6])
	dscale := int16(binary.BigEndian.Uint16(b[6:8]))

	if sign == numericNan {
		return storage.ZeroDecimal(0), nil
	}
	if ndigits == 0 {
		return storage.ZeroDecimal(int32(dscale)), nil
	}
	if len(b) < 8+int(ndigits)*2 {
		return storage.ZeroDecimal(0), fmt.Errorf("numeric payload truncated: expected %d bytes, got %d", 8+int(ndigits)*2, len(b))
	}

	val := big.NewInt(0)
	big10000 := big.NewInt(10000)

	for i := int16(0); i < ndigits; i++ {
		offset := 8 + int(i)*2
		digit := int16(binary.BigEndian.Uint16(b[offset : offset+2]))
		val.Mul(val, big10000)
		val.Add(val, big.NewInt(int64(digit)))
	}

	power := 4 * int(weight-ndigits+1)
	scale := int32(0)

	if power >= 0 {
		if power > 0 {
			mult := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil)
			val.Mul(val, mult)
		}
		if dscale > 0 {
			mult := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dscale)), nil)
			val.Mul(val, mult)
			scale = int32(dscale)
		}
	} else {
		scale = int32(-power)
		if int(dscale) > int(scale) {
			diff := int(dscale) - int(scale)
			mult := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
			val.Mul(val, mult)
			scale = int32(dscale)
		} else if int(dscale) < int(scale) {
			diff := int(scale) - int(dscale)
			div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
			rem := new(big.Int)
			quot, rem := new(big.Int).QuoRem(val, div, rem)
			if rem.Sign() == 0 {
				val = quot
				scale = int32(dscale)
			}
		}
	}

	if sign == numericNeg {
		val.Neg(val)
	}

	return storage.NewDecimal(val, scale), nil
}

// encodePgBinaryNumeric encodes a decimal / numeric value into PostgreSQL binary NUMERIC (OID 1700) bytes.
func encodePgBinaryNumeric(val any) []byte {
	if val == nil {
		return nil
	}

	var dec storage.Decimal
	switch v := val.(type) {
	case storage.Decimal:
		dec = v
	case *storage.Decimal:
		if v == nil {
			return nil
		}
		dec = *v
	case storage.Value:
		if v.IsNull {
			return nil
		}
		if v.Type == storage.TypeDecimal {
			dec = v.DVal
		} else {
			parsed, err := storage.ParseDecimal(v.String())
			if err != nil {
				return encodeZeroNumeric(0)
			}
			dec = parsed
		}
	case *storage.Value:
		if v == nil || v.IsNull {
			return nil
		}
		if v.Type == storage.TypeDecimal {
			dec = v.DVal
		} else {
			parsed, err := storage.ParseDecimal(v.String())
			if err != nil {
				return encodeZeroNumeric(0)
			}
			dec = parsed
		}
	case int:
		dec = storage.NewDecimalFromInt(int64(v), 0)
	case int32:
		dec = storage.NewDecimalFromInt(int64(v), 0)
	case int64:
		dec = storage.NewDecimalFromInt(v, 0)
	case float64:
		dec = storage.NewDecimalFromFloat(v, 2)
	case float32:
		dec = storage.NewDecimalFromFloat(float64(v), 2)
	case string:
		parsed, err := storage.ParseDecimal(v)
		if err != nil {
			return encodeZeroNumeric(0)
		}
		dec = parsed
	default:
		parsed, err := storage.ParseDecimal(fmt.Sprintf("%v", val))
		if err != nil {
			return encodeZeroNumeric(0)
		}
		dec = parsed
	}

	str := dec.String()
	isNeg := false
	if strings.HasPrefix(str, "-") {
		isNeg = true
		str = str[1:]
	}

	intPart := str
	fracPart := ""
	if dot := strings.IndexByte(str, '.'); dot != -1 {
		intPart = str[:dot]
		fracPart = str[dot+1:]
	}

	dscale := dec.Scale()
	if dscale < int32(len(fracPart)) {
		dscale = int32(len(fracPart))
	}

	// Clean leading zeros on intPart
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}

	// Check if entire number is zero
	fracTrimmed := strings.TrimRight(fracPart, "0")
	if intPart == "0" && fracTrimmed == "" {
		return encodeZeroNumeric(uint16(dscale))
	}

	var digits []int16
	weight := int16(0)

	if intPart != "0" {
		// Group integer part from right to left in chunks of 4
		var intChunks []int16
		rem := intPart
		for len(rem) > 0 {
			start := len(rem) - 4
			if start < 0 {
				start = 0
			}
			chunkStr := rem[start:]
			chunkVal, _ := strconv.ParseInt(chunkStr, 10, 16)
			intChunks = append(intChunks, int16(chunkVal))
			rem = rem[:start]
		}
		weight = int16(len(intChunks) - 1)
		for i := len(intChunks) - 1; i >= 0; i-- {
			digits = append(digits, intChunks[i])
		}
	}

	if fracPart != "" {
		// Pad fracPart with zeros to multiple of 4
		paddedFrac := fracPart
		if rem := len(paddedFrac) % 4; rem != 0 {
			paddedFrac += strings.Repeat("0", 4-rem)
		}

		firstNonZeroFracChunk := -1
		var fracChunks []int16
		for i := 0; i < len(paddedFrac); i += 4 {
			chunkStr := paddedFrac[i : i+4]
			chunkVal, _ := strconv.ParseInt(chunkStr, 10, 16)
			if chunkVal != 0 && firstNonZeroFracChunk == -1 {
				firstNonZeroFracChunk = len(fracChunks)
			}
			fracChunks = append(fracChunks, int16(chunkVal))
		}

		if intPart == "0" && firstNonZeroFracChunk != -1 {
			// No integer part, weight determined by leading zero chunks in frac
			weight = -int16(firstNonZeroFracChunk + 1)
			digits = append(digits, fracChunks[firstNonZeroFracChunk:]...)
		} else if intPart != "0" {
			digits = append(digits, fracChunks...)
		}
	}

	// Strip trailing zero chunks from digits
	for len(digits) > 0 && digits[len(digits)-1] == 0 {
		digits = digits[:len(digits)-1]
	}

	if len(digits) == 0 {
		return encodeZeroNumeric(uint16(dscale))
	}

	sign := uint16(numericPos)
	if isNeg {
		sign = numericNeg
	}

	buf := make([]byte, 8+len(digits)*2)
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(digits)))
	binary.BigEndian.PutUint16(buf[2:4], uint16(weight))
	binary.BigEndian.PutUint16(buf[4:6], sign)
	binary.BigEndian.PutUint16(buf[6:8], uint16(dscale))

	for i, d := range digits {
		binary.BigEndian.PutUint16(buf[8+i*2:10+i*2], uint16(d))
	}

	return buf
}

func encodeZeroNumeric(dscale uint16) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint16(buf[0:2], 0)
	binary.BigEndian.PutUint16(buf[2:4], 0)
	binary.BigEndian.PutUint16(buf[4:6], numericPos)
	binary.BigEndian.PutUint16(buf[6:8], dscale)
	return buf
}
