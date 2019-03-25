package bexpr

import (
	"fmt"
	"reflect"
	"strings"
)

var primitiveEqualityFns = map[reflect.Kind]func(first interface{}, second interface{}) bool{
	reflect.Bool:    doEqualBool,
	reflect.Int:     doEqualInt,
	reflect.Int8:    doEqualInt8,
	reflect.Int16:   doEqualInt16,
	reflect.Int32:   doEqualInt32,
	reflect.Int64:   doEqualInt64,
	reflect.Uint:    doEqualUint,
	reflect.Uint8:   doEqualUint8,
	reflect.Uint16:  doEqualUint16,
	reflect.Uint32:  doEqualUint32,
	reflect.Uint64:  doEqualUint64,
	reflect.Float32: doEqualFloat32,
	reflect.Float64: doEqualFloat64,
	reflect.String:  doEqualString,
}

func doEqualBool(first interface{}, second interface{}) bool {
	return first.(bool) == second.(bool)
}

func doEqualInt(first interface{}, second interface{}) bool {
	return first.(int) == second.(int)
}

func doEqualInt8(first interface{}, second interface{}) bool {
	return first.(int8) == second.(int8)
}

func doEqualInt16(first interface{}, second interface{}) bool {
	return first.(int16) == second.(int16)
}

func doEqualInt32(first interface{}, second interface{}) bool {
	return first.(int32) == second.(int32)
}

func doEqualInt64(first interface{}, second interface{}) bool {
	return first.(int64) == second.(int64)
}

func doEqualUint(first interface{}, second interface{}) bool {
	return first.(uint) == second.(uint)
}

func doEqualUint8(first interface{}, second interface{}) bool {
	return first.(uint8) == second.(uint8)
}

func doEqualUint16(first interface{}, second interface{}) bool {
	return first.(uint16) == second.(uint16)
}

func doEqualUint32(first interface{}, second interface{}) bool {
	return first.(uint32) == second.(uint32)
}

func doEqualUint64(first interface{}, second interface{}) bool {
	return first.(uint64) == second.(uint64)
}

func doEqualFloat32(first interface{}, second interface{}) bool {
	return first.(float32) == second.(float32)
}

func doEqualFloat64(first interface{}, second interface{}) bool {
	return first.(float64) == second.(float64)
}

func doEqualString(first interface{}, second interface{}) bool {
	return first.(string) == second.(string)
}

// Get rid of 0 to many levels of pointers to get at the real type
func derefType(rtype reflect.Type) reflect.Type {
	for rtype.Kind() == reflect.Ptr {
		rtype = rtype.Elem()
	}
	return rtype
}

func doMatchEqual(expression *MatchExpr, value reflect.Value) (bool, error) {
	// NOTE: see preconditions in evaluateMatchExpressionRecurse
	eqFn := primitiveEqualityFns[value.Kind()]
	matchValue := getMatchExprValue(expression)
	return eqFn(matchValue, value.Interface()), nil
}

func doMatchIn(expression *MatchExpr, value reflect.Value) (bool, error) {
	// NOTE: see preconditions in evaluateMatchExpressionRecurse
	matchValue := getMatchExprValue(expression)

	switch kind := value.Kind(); kind {
	case reflect.Map:
		found := value.MapIndex(reflect.ValueOf(matchValue))
		return found.IsValid(), nil
	case reflect.Slice, reflect.Array:
		itemType := derefType(value.Type().Elem())
		eqFn := primitiveEqualityFns[itemType.Kind()]

		for i := 0; i < value.Len(); i++ {
			item := value.Index(i)

			// the value will be the correct type as we verified the itemType
			if eqFn(matchValue, reflect.Indirect(item).Interface()) {
				return true, nil
			}
		}

		return false, nil
	case reflect.String:
		return strings.Contains(value.String(), matchValue.(string)), nil
	default:
		// this shouldn't be possible but we have to have something to return to keep the compiler happy
		return false, fmt.Errorf("Cannot perform in/contains operations on type %s for selector: %q", kind, expression.Selector)
	}
}

func doMatchIsEmpty(matcher *MatchExpr, value reflect.Value) (bool, error) {
	// NOTE: see preconditions in evaluateMatchExpressionRecurse
	return value.Len() == 0, nil
}

func getMatchExprValue(expression *MatchExpr) interface{} {
	// NOTE: see preconditions in evaluateMatchExpressionRecurse
	if expression.Value == nil {
		return nil
	}

	if expression.Value.Converted != nil {
		return expression.Value.Converted
	}

	return expression.Value.Raw
}

func evaluateMatchExpressionRecurse(expression *MatchExpr, depth int, rvalue reflect.Value, fields FieldConfigurations) (bool, error) {
	// NOTE: Some information about preconditions is probably good to have here. Parsing
	//       as well as the extra validation pass that MUST occur before executing the
	//       expression evaluation allow us to make some assumptions here.
	//
	//       1. Selectors MUST be valid. Therefore we don't need to test if they should
	//          be valid. This means that we can index in the FieldConfigurations map
	//          and a configuration MUST be present.
	//       2. If expression.Value could be converted it will already have been. No need to try
	//          and convert again. There is also no need to check that the types match as they MUST
	//          in order to have passed validation.
	//       3. If we are presented with a map and we have more selectors to go through then its key
	//          type MUST be a string
	//       4. We already have validated that the operations can be performed on the target data.
	//          So calls to the doMatch* functions don't need to do any checking to ensure that
	//          calling various fns on them will work and not panic - because they wont.

	if depth >= len(expression.Selector) {
		// we have reached the end of the selector - execute the match operations
		switch expression.Operator {
		case MatchEqual:
			return doMatchEqual(expression, rvalue)
		case MatchNotEqual:
			result, err := doMatchEqual(expression, rvalue)
			if err == nil {
				return !result, nil
			}
			return false, err
		case MatchIn:
			return doMatchIn(expression, rvalue)
		case MatchNotIn:
			result, err := doMatchIn(expression, rvalue)
			if err == nil {
				return !result, nil
			}
			return false, err
		case MatchIsEmpty:
			return doMatchIsEmpty(expression, rvalue)
		case MatchIsNotEmpty:
			result, err := doMatchIsEmpty(expression, rvalue)
			if err == nil {
				return !result, nil
			}
			return false, err
		default:
			return false, fmt.Errorf("Invalid match operation: %d", expression.Operator)
		}
	}

	switch rvalue.Kind() {
	case reflect.Struct:
		fieldName := expression.Selector[depth]
		fieldConfig := fields[FieldName(fieldName)]

		if fieldConfig.StructFieldName != "" {
			fieldName = fieldConfig.StructFieldName
		}

		value := reflect.Indirect(rvalue.FieldByName(fieldName))

		if matcher, ok := value.Interface().(ExpressionEvaluator); ok {
			return matcher.EvaluateMatch(expression.Selector[depth+1:], expression.Operator, getMatchExprValue(expression))
		}

		return evaluateMatchExpressionRecurse(expression, depth+1, value, fieldConfig.SubFields)

	case reflect.Slice, reflect.Array:
		// TODO (mkeeler) - Should we support implementing the ExpressionEvaluator interface for slice/array types?
		//                  Punting on that for now.
		for i := 0; i < rvalue.Len(); i++ {
			item := reflect.Indirect(rvalue.Index(i))
			// we use the same depth because right now we are not allowing
			// selection of individual slice/array elements
			result, err := evaluateMatchExpressionRecurse(expression, depth, item, fields)
			if err != nil {
				return false, err
			}

			// operations on slices are implicity ANY operations currently so the first truthy evaluation we find we can stop
			if result {
				return true, nil
			}
		}

		return false, nil
	case reflect.Map:
		// TODO (mkeeler) - Should we support implementing the ExpressionEvaluator interface for map types
		//                  such as the FieldConfigurations type? Maybe later
		//
		value := reflect.Indirect(rvalue.MapIndex(reflect.ValueOf(expression.Selector[depth])))

		if !value.IsValid() {
			// when the key doesn't exist in the map
			switch expression.Operator {
			case MatchEqual, MatchIsNotEmpty, MatchIn:
				return false, nil
			default:
				// MatchNotEqual, MatchIsEmpty, MatchNotIn
				// Whatever you were looking for cannot be equal because it doesn't exist
				// Similarly it cannot be in some other container and every other container
				// is always empty.
				return true, nil
			}
		}

		if matcher, ok := value.Interface().(ExpressionEvaluator); ok {
			return matcher.EvaluateMatch(expression.Selector[depth+1:], expression.Operator, getMatchExprValue(expression))
		}

		return evaluateMatchExpressionRecurse(expression, depth+1, value, fields[FieldNameAny].SubFields)
	default:
		return false, fmt.Errorf("Value at selector %q with type %s does not support nested field selection", expression.Selector[:depth], rvalue.Kind())
	}
}

func evaluateMatchExpression(expression *MatchExpr, datum interface{}, fields FieldConfigurations) (bool, error) {
	if matcher, ok := datum.(ExpressionEvaluator); ok {
		return matcher.EvaluateMatch(expression.Selector, expression.Operator, getMatchExprValue(expression))
	}

	rvalue := reflect.Indirect(reflect.ValueOf(datum))

	return evaluateMatchExpressionRecurse(expression, 0, rvalue, fields)
}

func evaluate(ast Expr, datum interface{}, fields FieldConfigurations) (bool, error) {
	switch node := ast.(type) {
	case *UnaryExpr:
		switch node.Operator {
		case UnaryOpNot:
			result, err := evaluate(node.Operand, datum, fields)
			return !result, err
		}
	case *BinaryExpr:
		switch node.Operator {
		case BinaryOpAnd:
			result, err := evaluate(node.Left, datum, fields)
			if err != nil || result == false {
				return result, err
			}

			return evaluate(node.Right, datum, fields)

		case BinaryOpOr:
			result, err := evaluate(node.Left, datum, fields)
			if err != nil || result == true {
				return result, err
			}

			return evaluate(node.Right, datum, fields)
		}
	case *MatchExpr:
		return evaluateMatchExpression(node, datum, fields)
	}
	return false, fmt.Errorf("Invalid AST node")
}
