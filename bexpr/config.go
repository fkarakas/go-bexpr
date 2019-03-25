package bexpr

import (
	"fmt"
	"reflect"
	"strings"
)

// Function type for usage with a SelectorConfiguration
type FieldValueCoercionFn func(value string) (interface{}, error)

// Strongly typed name of a field
type FieldName string

// Used to represent an arbitrary field name
const FieldNameAny FieldName = ""

// The FieldConfiguration struct represents how boolean expression
// validation and preparation should work for the given field. A field
// in this case is a single element of a selector.
//
// Example: foo.bar.baz has 3 fields separate by '.' characters.
type FieldConfiguration struct {
	// Name to use when looking up fields within a struct. This is useful when
	// the name(s) you want to expose to users writing the expressions does not
	// exactly match the Field name of the structure. If this is empty then the
	// user provided name will be used
	StructFieldName string

	// Nested field configurations
	SubFields FieldConfigurations

	// Function to run on the raw string value present in the expression
	// syntax to coerce into whatever form the ExpressionEvaluator wants
	// The coercion happens only once and will then be passed as the `value`
	// parameter to all EvaluateMatch invocations on the ExpressionEvaluator.
	CoerceFn FieldValueCoercionFn

	// List of MatchOperators supported for this field. This configuration
	// is used to pre-validate an expressions fields before execution.
	SupportedOperations []MatchOperator
}

// Represents all the valid fields and their corresponding configuration
type FieldConfigurations map[FieldName]*FieldConfiguration

// Extra configuration used to perform further validation on a parsed
// expression and to aid in the evaluation process
type EvaluatorConfig struct {
	// Maximum number of matching expressions allowed. 0 means unlimited
	// This does not include and, or and not expressions within the AST
	MaxMatches int
	// Maximum length of raw values. 0 means unlimited
	MaxRawValueLength int
}

func generateFieldConfigurationInternal(rtype reflect.Type) (*FieldConfiguration, error) {
	// Handle those types that implement our interface
	if rtype.Implements(reflect.TypeOf((*ExpressionEvaluator)(nil)).Elem()) {

		// TODO (mkeeler) Do we need to new a value just to call the function? Potentially we can
		// lookup the func and invoke it with a nil pointer?
		value := reflect.New(rtype)
		configs := value.Interface().(ExpressionEvaluator).FieldConfigurations()

		return &FieldConfiguration{
			SubFields: configs,
		}, nil
	}

	// Handle primitive types
	if coerceFn, ok := primitiveCoercionFns[rtype.Kind()]; ok {
		return &FieldConfiguration{
			CoerceFn:            coerceFn,
			SupportedOperations: []MatchOperator{MatchEqual, MatchNotEqual},
		}, nil
	}

	// Handle compound types
	switch rtype.Kind() {
	case reflect.Map:
		return generateMapFieldConfiguration(derefType(rtype.Key()), derefType(rtype.Elem()))
	case reflect.Array, reflect.Slice:
		return generateSliceFieldConfiguration(derefType(rtype.Elem()))
	case reflect.Struct:
		subfields, err := generateStructFieldConfigurations(rtype)
		if err != nil {
			return nil, err
		}

		return &FieldConfiguration{
			SubFields: subfields,
		}, nil

	default: // unsupported types are just not filterable
		return nil, nil
	}
}

func generateSliceFieldConfiguration(elemType reflect.Type) (*FieldConfiguration, error) {
	if coerceFn, ok := primitiveCoercionFns[elemType.Kind()]; ok {
		// slices of primitives have somewhat different supported operations
		return &FieldConfiguration{
			CoerceFn:            coerceFn,
			SupportedOperations: []MatchOperator{MatchIn, MatchNotIn, MatchIsEmpty, MatchIsNotEmpty},
		}, nil
	}

	subfield, err := generateFieldConfigurationInternal(elemType)
	if err != nil {
		return nil, err
	}

	cfg := &FieldConfiguration{
		SupportedOperations: []MatchOperator{MatchIsEmpty, MatchIsNotEmpty},
	}

	if subfield != nil && len(subfield.SubFields) > 0 {
		cfg.SubFields = subfield.SubFields
	}

	return cfg, nil
}

func generateMapFieldConfiguration(keyType, valueType reflect.Type) (*FieldConfiguration, error) {
	switch keyType.Kind() {
	case reflect.String:
		subfield, err := generateFieldConfigurationInternal(valueType)
		if err != nil {
			return nil, err
		}

		cfg := &FieldConfiguration{
			CoerceFn:            CoerceString,
			SupportedOperations: []MatchOperator{MatchIsEmpty, MatchIsNotEmpty, MatchIn, MatchNotIn},
		}

		if subfield != nil {
			cfg.SubFields = FieldConfigurations{
				FieldNameAny: subfield,
			}
		}

		return cfg, nil

	default:
		// For maps with non-string keys we can really only do emptiness checks
		// and cannot index into them at all
		return &FieldConfiguration{
			SupportedOperations: []MatchOperator{MatchIsEmpty, MatchIsNotEmpty},
		}, nil
	}
}

func generateStructFieldConfigurations(rtype reflect.Type) (FieldConfigurations, error) {
	fieldConfigs := make(FieldConfigurations)

	for i := 0; i < rtype.NumField(); i++ {
		field := rtype.Field(i)

		fieldTag := field.Tag.Get("bexpr")

		var fieldNames []string

		if field.PkgPath != "" {
			// we cant handle unexported fields using reflection
			continue
		}

		if fieldTag != "" {
			parts := strings.Split(fieldTag, ",")

			if len(parts) > 0 {
				if parts[0] == "-" {
					continue
				}

				fieldNames = parts
			} else {
				fieldNames = append(fieldNames, field.Name)
			}
		} else {
			fieldNames = append(fieldNames, field.Name)
		}

		cfg, err := generateFieldConfigurationInternal(derefType(field.Type))
		if err != nil {
			return nil, err
		}
		cfg.StructFieldName = field.Name

		// link the config to all the correct names
		for _, name := range fieldNames {
			fieldConfigs[FieldName(name)] = cfg
		}
	}

	return fieldConfigs, nil
}

// `generateFieldConfigurations` can be used to generate the `FieldConfigurations` map
// It supports generating configurations for either a `map[string]*` or a `struct` as the `topLevelType`
//
// Internally within the top level type the following is supported:
//
// Primitive Types:
//    strings
//    integers (all width types and signedness)
//    floats (32 and 64 bit)
//    bool
//
// Compound Types
//   `map[*]*`
//       - Supports emptiness checking. Does not support further selector nesting.
//   `map[string]*`
//       - Supports in/contains operations on the keys.
//   `map[string]<supported type>`
//       - Will have a single subfield with name `FieldNameAny` (wildcard) and the rest of
//         the field configuration will come from the `<supported type>`
//   `[]*`
//       - Supports emptiness checking only. Does not support further selector nesting.
//   `[]<supported primitive type>`
//       - Supports in/contains operations against the primitive values.
//   `[]<supported compund type>`
//       - Will have subfields with the configuration of whatever the supported
//         compound type is.
//       - Does not support indexing of individual values like a map does currently
//         and with the current evaluation logic slices of slices will mostly be
//         handled as if they were flattened. One thing that cannot be done is
//         to be able to perform emptiness/contains checking against the internal
//         slice.
//   structs
//       - No operations are supported on the struct itself
//       - Will have subfield configurations generated for the fields of the struct.
//       - A struct tag like `bexpr:"<name>"` allows changing the name that allows indexing
//         into the subfield.
//       - By default unexported fields of a struct are not selectable. If The struct tag is
//         present then this behavior is overridden.
//       - Exported fields can be made unselectable by adding a tag to the field like `bexpr:"-"`
func GenerateFieldConfigurations(topLevelType interface{}) (FieldConfigurations, error) {
	fields, _, err := generateFieldConfigurationsAndType(topLevelType)
	return fields, err
}

func generateFieldConfigurationsAndType(topLevelType interface{}) (FieldConfigurations, reflect.Type, error) {
	rtype := derefType(reflect.TypeOf(topLevelType))

	if expressionEval, ok := topLevelType.(ExpressionEvaluator); ok {
		return expressionEval.FieldConfigurations(), rtype, nil
	}

	switch rtype.Kind() {
	case reflect.Struct:
		fields, err := generateStructFieldConfigurations(rtype)
		return fields, rtype, err
	case reflect.Map:
		if rtype.Key().Kind() != reflect.String {
			return nil, rtype, fmt.Errorf("Cannot generate FieldConfigurations for maps with keys that are not strings")
		}

		elemType := derefType(rtype.Elem())

		field, err := generateFieldConfigurationInternal(elemType)
		if err != nil {
			return nil, rtype, err
		}

		if field == nil {
			return nil, rtype, nil
		}

		return FieldConfigurations{
			FieldNameAny: field,
		}, rtype, nil
	}

	return nil, rtype, fmt.Errorf("Invalid top level type - can only use structs, map[string]* or an ExpressionEvaluator")
}
