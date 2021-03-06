package query

import (
	"bytes"
	"strings"

	"github.com/mithrandie/csvq/lib/cmd"

	"github.com/mithrandie/csvq/lib/parser"
	"github.com/mithrandie/csvq/lib/value"

	"github.com/mithrandie/ternary"
)

type SortValueType int

const (
	NullType SortValueType = iota
	IntegerType
	FloatType
	DatetimeType
	BooleanType
	StringType
)

type SortValues []*SortValue

func (values SortValues) Less(compareValues SortValues, directions []int, nullPositions []int) bool {
	for i, val := range values {
		t := val.Less(compareValues[i])
		if t != ternary.UNKNOWN {
			if directions[i] == parser.ASC {
				return t == ternary.TRUE
			} else {
				return t == ternary.FALSE
			}
		}

		if val.Type == NullType && compareValues[i].Type != NullType {
			if nullPositions[i] == parser.FIRST {
				return true
			} else {
				return false
			}
		}
		if val.Type != NullType && compareValues[i].Type == NullType {
			if nullPositions[i] == parser.FIRST {
				return false
			} else {
				return true
			}
		}
	}
	return false
}

func (values SortValues) EquivalentTo(compareValues SortValues) bool {
	if compareValues == nil {
		return false
	}

	for i, val := range values {
		if !val.EquivalentTo(compareValues[i]) {
			return false
		}
	}
	return true
}

func (values SortValues) Serialize(buf *bytes.Buffer) {
	for i, val := range values {
		if 0 < i {
			buf.WriteRune(':')
		}

		switch val.Type {
		case NullType:
			serializeNull(buf)
		case IntegerType:
			serializeInteger(buf, val.Integer)
		case FloatType:
			serializeFlaot(buf, val.Float)
		case DatetimeType:
			serializeDatetimeFromUnixNano(buf, val.Datetime)
		case BooleanType:
			serializeBoolean(buf, val.Boolean)
		case StringType:
			serializeString(buf, val.String)
		}
	}
}

type SortValue struct {
	Type SortValueType

	Integer  int64
	Float    float64
	Datetime int64
	String   string
	Boolean  bool
}

func NewSortValue(val value.Primary, flags *cmd.Flags) *SortValue {
	sortValue := &SortValue{}

	if value.IsNull(val) {
		sortValue.Type = NullType
	} else if i := value.ToInteger(val); !value.IsNull(i) {
		s := value.ToString(val)
		sortValue.Type = IntegerType
		sortValue.Integer = i.(value.Integer).Raw()
		sortValue.Float = float64(sortValue.Integer)
		sortValue.Datetime = sortValue.Integer * 1e9
		sortValue.String = s.(value.String).Raw()
	} else if f := value.ToFloat(val); !value.IsNull(f) {
		s := value.ToString(val)
		sortValue.Type = FloatType
		sortValue.Float = f.(value.Float).Raw()
		sortValue.Datetime = int64(sortValue.Float * 1e9)
		sortValue.String = s.(value.String).Raw()
	} else if dt := value.ToDatetime(val, flags.DatetimeFormat); !value.IsNull(dt) {
		t := dt.(value.Datetime).Raw()
		if t.Nanosecond() > 0 {
			f := float64(t.Unix()) + float64(t.Nanosecond())/1e9
			t2 := value.Float64ToTime(f)
			if t.Equal(t2) {
				sortValue.Type = FloatType
				sortValue.Float = f
				sortValue.Datetime = t.UnixNano()
				sortValue.String = value.Float64ToStr(f)
			} else {
				sortValue.Type = DatetimeType
				sortValue.Datetime = t.UnixNano()
			}
		} else {
			sortValue.Type = IntegerType
			i := t.Unix()
			sortValue.Integer = i
			sortValue.Float = float64(i)
			sortValue.Datetime = t.UnixNano()
			sortValue.String = value.Int64ToStr(i)
		}
	} else if b := value.ToBoolean(val); !value.IsNull(b) {
		sortValue.Type = BooleanType
		sortValue.Boolean = b.(value.Boolean).Raw()
		if sortValue.Boolean {
			sortValue.Integer = 1
		} else {
			sortValue.Integer = 0
		}
	} else if s, ok := val.(value.String); ok {
		sortValue.Type = StringType
		sortValue.String = strings.ToUpper(strings.TrimSpace(s.Raw()))
	} else {
		sortValue.Type = NullType
	}

	return sortValue
}

func (v *SortValue) Less(compareValue *SortValue) ternary.Value {
	switch v.Type {
	case IntegerType:
		switch compareValue.Type {
		case IntegerType:
			if v.Integer == compareValue.Integer {
				return ternary.UNKNOWN
			}
			return ternary.ConvertFromBool(v.Integer < compareValue.Integer)
		case FloatType:
			return ternary.ConvertFromBool(v.Float < compareValue.Float)
		case DatetimeType:
			return ternary.ConvertFromBool(v.Datetime < compareValue.Datetime)
		case StringType:
			return ternary.ConvertFromBool(v.String < compareValue.String)
		}
	case FloatType:
		switch compareValue.Type {
		case IntegerType, FloatType:
			if v.Float == compareValue.Float {
				return ternary.UNKNOWN
			}
			return ternary.ConvertFromBool(v.Float < compareValue.Float)
		case DatetimeType:
			return ternary.ConvertFromBool(v.Datetime < compareValue.Datetime)
		case StringType:
			return ternary.ConvertFromBool(v.String < compareValue.String)
		}
	case DatetimeType:
		switch compareValue.Type {
		case IntegerType, FloatType, DatetimeType:
			if v.Datetime == compareValue.Datetime {
				return ternary.UNKNOWN
			}
			return ternary.ConvertFromBool(v.Datetime < compareValue.Datetime)
		}
	case StringType:
		switch compareValue.Type {
		case IntegerType, FloatType, StringType:
			if v.String == compareValue.String {
				return ternary.UNKNOWN
			}
			return ternary.ConvertFromBool(v.String < compareValue.String)
		}
	}

	return ternary.UNKNOWN
}

func (v *SortValue) EquivalentTo(compareValue *SortValue) bool {
	switch v.Type {
	case IntegerType:
		switch compareValue.Type {
		case IntegerType, BooleanType:
			return v.Integer == compareValue.Integer
		}
	case FloatType:
		switch compareValue.Type {
		case FloatType:
			return v.Float == compareValue.Float
		}
	case DatetimeType:
		switch compareValue.Type {
		case DatetimeType:
			return v.Datetime == compareValue.Datetime
		}
	case BooleanType:
		switch compareValue.Type {
		case IntegerType:
			return v.Integer == compareValue.Integer
		case BooleanType:
			return v.Boolean == compareValue.Boolean
		}
	case StringType:
		switch compareValue.Type {
		case StringType:
			return v.String == compareValue.String
		}
	case NullType:
		return compareValue.Type == NullType
	}

	return false
}
