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
