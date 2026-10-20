package proc

import (
	"math"
	"math/bits"
	mathrand "math/rand/v2"

	"lionsql/pkg/storage"
)


func isMathBuiltin(name string) bool {
	switch name {
	case "ABS", "ROUND", "CEILING", "CEIL", "FLOOR", "POWER", "SQRT", "SQUARE", "SIGN", "PI",
		"EXP", "LOG", "LOG10", "SIN", "COS", "TAN", "COT", "ASIN", "ACOS", "ATAN", "ATN2", "DEGREES", "RADIANS", "RAND", "RANDOM",
		"BIT_COUNT", "GET_BIT", "SET_BIT", "LEFT_SHIFT", "RIGHT_SHIFT":
		return true
	default:
		return false
	}
}

func evalMathBuiltin(upper string, args []storage.Value) storage.Value {
	switch upper {
	// 3. Mathematical Functions
	// ----------------------------------------------------
	case "ABS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			absVal := math.Abs(n)
			if absVal == math.Trunc(absVal) {
				return storage.IntValue(int64(absVal))
			}
			return storage.FloatValue(absVal)
		}
		return storage.NullValue(storage.TypeFloat)

	case "ROUND":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			decimals := 0
			if len(args) >= 2 && !args[1].IsNull {
				decimals = int(args[1].IVal)
			}
			factor := math.Pow(10, float64(decimals))
			rounded := math.Round(n*factor) / factor
			if decimals == 0 {
				return storage.IntValue(int64(rounded))
			}
			return storage.FloatValue(rounded)
		}
		return storage.NullValue(storage.TypeFloat)

	case "CEILING", "CEIL":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.IntValue(int64(math.Ceil(n)))
		}
		return storage.NullValue(storage.TypeInt)

	case "FLOOR":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.IntValue(int64(math.Floor(n)))
		}
		return storage.NullValue(storage.TypeInt)

	case "POWER":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		b, ok1 := toFloatNum(args[0])
		e, ok2 := toFloatNum(args[1])
		if !ok1 || !ok2 {
			return storage.NullValue(storage.TypeFloat)
		}
		res := math.Pow(b, e)
		if res == math.Trunc(res) && math.Abs(res) < float64(math.MaxInt64) {
			return storage.IntValue(int64(res))
		}
		return storage.FloatValue(res)

	case "SQRT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n < 0 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Sqrt(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "SQUARE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if args[0].Type == storage.TypeInt {
			v := args[0].IVal
			return storage.IntValue(v * v)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(n * n)
		}
		return storage.NullValue(storage.TypeFloat)

	case "SIGN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n > 0 {
				return storage.IntValue(1)
			} else if n < 0 {
				return storage.IntValue(-1)
			}
			return storage.IntValue(0)
		}
		return storage.NullValue(storage.TypeInt)

	case "PI":
		return storage.FloatValue(math.Pi)

	case "EXP":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Exp(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "LOG":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		n, ok := toFloatNum(args[0])
		if !ok || n <= 0 {
			return storage.NullValue(storage.TypeFloat)
		}
		if len(args) >= 2 && !args[1].IsNull {
			base, okBase := toFloatNum(args[1])
			if !okBase || base <= 0 || base == 1 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Log(n) / math.Log(base))
		}
		return storage.FloatValue(math.Log(n))

	case "LOG10":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		n, ok := toFloatNum(args[0])
		if !ok || n <= 0 {
			return storage.NullValue(storage.TypeFloat)
		}
		return storage.FloatValue(math.Log10(n))

	case "SIN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Sin(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "COS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Cos(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "TAN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Tan(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "COT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			tanVal := math.Tan(n)
			if tanVal == 0 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(1.0 / tanVal)
		}
		return storage.NullValue(storage.TypeFloat)

	case "ASIN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n < -1 || n > 1 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Asin(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "ACOS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			if n < -1 || n > 1 {
				return storage.NullValue(storage.TypeFloat)
			}
			return storage.FloatValue(math.Acos(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "ATAN":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(math.Atan(n))
		}
		return storage.NullValue(storage.TypeFloat)

	case "ATN2":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		y, okY := toFloatNum(args[0])
		x, okX := toFloatNum(args[1])
		if !okY || !okX {
			return storage.NullValue(storage.TypeFloat)
		}
		return storage.FloatValue(math.Atan2(y, x))

	case "DEGREES":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(n * 180.0 / math.Pi)
		}
		return storage.NullValue(storage.TypeFloat)

	case "RADIANS":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeFloat)
		}
		if n, ok := toFloatNum(args[0]); ok {
			return storage.FloatValue(n * math.Pi / 180.0)
		}
		return storage.NullValue(storage.TypeFloat)

	case "RAND", "RANDOM":
		if len(args) > 0 && !args[0].IsNull {
			seedVal := args[0].IVal
			if args[0].Type == storage.TypeFloat {
				seedVal = int64(args[0].FVal)
			}
			r := mathrand.New(mathrand.NewPCG(uint64(seedVal), uint64(seedVal*6364136223846793005+1)))
			return storage.FloatValue(r.Float64())
		}
		return storage.FloatValue(mathrand.Float64())

	case "BIT_COUNT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := uint64(args[0].IVal)
		if args[0].Type == storage.TypeFloat {
			val = uint64(int64(args[0].FVal))
		}
		return storage.IntValue(int64(bits.OnesCount64(val)))

	case "GET_BIT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := uint64(args[0].IVal)
		offset := args[1].IVal
		if offset < 0 || offset > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		bit := (val >> offset) & 1
		return storage.IntValue(int64(bit))

	case "SET_BIT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := uint64(args[0].IVal)
		offset := args[1].IVal
		if offset < 0 || offset > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		bitVal := int64(1)
		if len(args) >= 3 && !args[2].IsNull {
			bitVal = args[2].IVal
		}
		if bitVal == 0 {
			val &^= (1 << offset)
		} else {
			val |= (1 << offset)
		}
		return storage.IntValue(int64(val))

	case "LEFT_SHIFT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := args[0].IVal
		shift := args[1].IVal
		if shift < 0 || shift > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(val << shift)

	case "RIGHT_SHIFT":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		val := args[0].IVal
		shift := args[1].IVal
		if shift < 0 || shift > 63 {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(val >> shift)

	// ----------------------------------------------------
	default:
		return storage.NullValue(storage.TypeUnknown)
	}
}
