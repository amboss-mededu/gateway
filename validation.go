package gateway

import (
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/validator/core"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

// noSchemaIntrospectionRule rejects the meta fields that expose the schema. __typename stays allowed
var noSchemaIntrospectionRule = core.Rule{
	Name: "NoSchemaIntrospection",
	RuleFunc: func(observers *core.Events, addError core.AddErrFunc) {
		observers.OnField(func(_ *core.Walker, field *ast.Field) {
			if field.Name == "__schema" || field.Name == "__type" {
				addError(
					core.Message(`GraphQL introspection is not allowed, but the query contained the field "%s".`, field.Name),
					core.At(field.Position),
				)
			}
		})
	},
}

// WithoutIntrospection returns an Option that rejects queries selecting __schema or __type
// during validation, so clients cannot pull the gateway's schema
func WithoutIntrospection() Option {
	return func(g *Gateway) {
		g.getValidationRules().AddRule(noSchemaIntrospectionRule.Name, noSchemaIntrospectionRule.RuleFunc)
	}
}

func (g *Gateway) getValidationRules() *rules.Rules {
	if g.validationRules == nil {
		g.validationRules = rules.NewDefaultRules()
	}
	return g.validationRules
}

// loadQuery parses the query and validates it against the schema with the gateway's rule set.
func (g *Gateway) loadQuery(schema *ast.Schema, query string) (*ast.QueryDocument, gqlerror.List) {
	if g == nil || g.validationRules == nil {
		return gqlparser.LoadQuery(schema, query)
	}
	return gqlparser.LoadQueryWithRules(schema, query, g.validationRules)
}

// suggestionRules pairs each default rule that appends "Did you mean …" hints to its errors
// with the variant that reports the same error without them
var suggestionRules = []struct {
	withSuggestions    core.Rule
	withoutSuggestions core.Rule
}{
	{rules.FieldsOnCorrectTypeRule, rules.FieldsOnCorrectTypeRuleWithoutSuggestions},
	{rules.KnownArgumentNamesRule, rules.KnownArgumentNamesRuleWithoutSuggestions},
	{rules.KnownTypeNamesRule, rules.KnownTypeNamesRuleWithoutSuggestions},
	{rules.ScalarLeafsRule, rules.ScalarLeafsRuleWithoutSuggestions},
	{rules.ValuesOfCorrectTypeRule, rules.ValuesOfCorrectTypeRuleWithoutSuggestions},
}

// WithoutFieldSuggestions returns an Option that drops the "Did you mean …" hints from
// validation errors. With introspection disabled, those hints would let clients guess
// field, argument, type and enum value names one typo at a time.
func WithoutFieldSuggestions() Option {
	return func(g *Gateway) {
		validationRules := g.getValidationRules()
		for _, rule := range suggestionRules {
			validationRules.RemoveRule(rule.withSuggestions.Name)
			validationRules.AddRule(rule.withoutSuggestions.Name, rule.withoutSuggestions.RuleFunc)
		}
	}
}
