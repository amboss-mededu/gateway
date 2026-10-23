package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nautilus/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newValidationTestGateway builds a gateway whose executor never reaches a remote service, so the
// handler tests only exercise parsing, validation and planning
func newValidationTestGateway(t *testing.T, options ...Option) *Gateway {
	t.Helper()
	schema, err := graphql.LoadSchema(`
		enum Role {
			ADMIN
			MEMBER
		}

		input UserFilter {
			role: Role
		}

		type User {
			name: String!
		}

		type Query {
			user(id: ID!): User
			users(filter: UserFilter): [User!]!
		}
	`)
	require.NoError(t, err)

	options = append(options, WithExecutor(ExecutorFunc(
		func(*ExecutionContext) (map[string]interface{}, error) {
			return map[string]interface{}{"executed": true}, nil
		},
	)))
	gateway, err := New([]*graphql.RemoteSchema{{Schema: schema, URL: "url1"}}, options...)
	require.NoError(t, err)

	return gateway
}

func postGraphQL(t *testing.T, gateway *Gateway, body string) (int, string) {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/graphql", strings.NewReader(body))
	responseRecorder := httptest.NewRecorder()
	gateway.GraphQLHandler(responseRecorder, request)

	response := responseRecorder.Result()
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	return response.StatusCode, string(responseBody)
}

func singleOperation(t *testing.T, query string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	return string(body)
}

func TestWithoutIntrospection_rejectsSchemaQueries(t *testing.T) {
	t.Parallel()
	gateway := newValidationTestGateway(t, WithoutIntrospection())

	tests := []struct {
		name          string
		body          string
		rejectedField string
	}{
		{
			name:          "__schema",
			body:          singleOperation(t, `{ __schema { queryType { name } } }`),
			rejectedField: "__schema",
		},
		{
			name:          "__type",
			body:          singleOperation(t, `{ __type(name: "User") { fields { name } } }`),
			rejectedField: "__type",
		},
		{
			name:          "aliased",
			body:          singleOperation(t, `{ schema: __schema { types { name } } }`),
			rejectedField: "__schema",
		},
		{
			name: "named fragment",
			body: singleOperation(t, `
				query { ...Introspection }
				fragment Introspection on Query { __schema { types { name } } }
			`),
			rejectedField: "__schema",
		},
		{
			name:          "inline fragment",
			body:          singleOperation(t, `{ ... on Query { __type(name: "User") { name } } }`),
			rejectedField: "__type",
		},
		{
			name:          "next to a regular field",
			body:          singleOperation(t, `{ user(id: "1") { name } __schema { types { name } } }`),
			rejectedField: "__schema",
		},
		{
			name: "inside a batch",
			body: `[
				{ "query": "{ user(id: \"1\") { name } }" },
				{ "query": "{ __schema { types { name } } }" }
			]`,
			rejectedField: "__schema",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			status, body := postGraphQL(t, gateway, test.body)

			assert.Equal(t, http.StatusBadRequest, status)
			var result resultWithErrors
			require.NoError(t, json.Unmarshal([]byte(body), &result))
			require.Len(t, result.Errors, 1)
			assert.Equal(t, "GRAPHQL_VALIDATION_FAILED", result.Errors[0].Extensions["code"])
			assert.Contains(t, result.Errors[0].Message,
				`GraphQL introspection is not allowed, but the query contained the field "`+test.rejectedField+`".`)
			assert.NotContains(t, body, "queryType")
		})
	}
}

func TestWithoutIntrospection_allowsRegularQueries(t *testing.T) {
	t.Parallel()
	gateway := newValidationTestGateway(t, WithoutIntrospection())

	tests := []struct {
		name  string
		query string
	}{
		{name: "regular field", query: `{ user(id: "1") { name } }`},
		{name: "__typename on an object", query: `{ user(id: "1") { __typename name } }`},
		{name: "__typename on the root", query: `{ __typename }`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			status, body := postGraphQL(t, gateway, singleOperation(t, test.query))

			assert.Equal(t, http.StatusOK, status)
			assert.JSONEq(t, `{"data":{"executed":true}}`, body)
		})
	}
}

func TestIntrospectionAllowedByDefault(t *testing.T) {
	t.Parallel()
	gateway := newValidationTestGateway(t)

	status, body := postGraphQL(t, gateway, singleOperation(t, `{ __schema { queryType { name } } }`))

	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"data":{"executed":true}}`, body)
}

func TestWithoutIntrospection_rejectsDirectQuery(t *testing.T) {
	t.Parallel()
	gateway := newValidationTestGateway(t, WithoutIntrospection())

	var result interface{}
	err := gateway.Query(context.Background(), &graphql.QueryInput{
		Query: `{ __type(name: "Query") { name } }`,
	}, &result)

	assert.ErrorContains(t, err, `GraphQL introspection is not allowed, but the query contained the field "__type".`)
	assert.Nil(t, result)
}

func TestWithoutFieldSuggestions(t *testing.T) {
	t.Parallel()
	gatewayWithSuggestions := newValidationTestGateway(t)
	gatewayWithoutSuggestions := newValidationTestGateway(t, WithoutFieldSuggestions())

	tests := []struct {
		name          string
		query         string
		expectedError string
	}{
		{
			name:          "misspelled field",
			query:         `{ usr(id: "1") { name } }`,
			expectedError: `Cannot query field "usr" on type "Query".`,
		},
		{
			name:          "misspelled argument",
			query:         `{ users(filtr: {}) { name } }`,
			expectedError: `Unknown argument "filtr" on field "Query.users".`,
		},
		{
			name:          "misspelled fragment type",
			query:         `{ user(id: "1") { ...UserName } } fragment UserName on Usr { name }`,
			expectedError: `Unknown type "Usr".`,
		},
		{
			name:          "missing selection",
			query:         `{ user(id: "1") }`,
			expectedError: `Field "user" of type "User" must have a selection of subfields.`,
		},
		{
			name:          "misspelled enum value",
			query:         `{ users(filter: { role: ADMN }) { name } }`,
			expectedError: `Value "ADMN" does not exist in "Role" enum.`,
		},
		{
			name:          "misspelled input field",
			query:         `{ users(filter: { rol: ADMIN }) { name } }`,
			expectedError: `Field "rol" is not defined by type "UserFilter".`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// the defaults suggest a fix, which is what the option has to remove
			_, bodyWithSuggestions := postGraphQL(t, gatewayWithSuggestions, singleOperation(t, test.query))
			assert.Contains(t, bodyWithSuggestions, "Did you mean")

			status, body := postGraphQL(t, gatewayWithoutSuggestions, singleOperation(t, test.query))

			assert.Equal(t, http.StatusBadRequest, status)
			var result resultWithErrors
			require.NoError(t, json.Unmarshal([]byte(body), &result))
			require.Len(t, result.Errors, 1)
			assert.Equal(t, "GRAPHQL_VALIDATION_FAILED", result.Errors[0].Extensions["code"])
			assert.Contains(t, result.Errors[0].Message, test.expectedError)
			assert.NotContains(t, body, "Did you mean")
		})
	}
}

func TestWithoutFieldSuggestions_combinedWithWithoutIntrospection(t *testing.T) {
	t.Parallel()
	gateway := newValidationTestGateway(t, WithoutIntrospection(), WithoutFieldSuggestions())

	status, body := postGraphQL(t, gateway, singleOperation(t, `{ __schema { types { name } } usr(id: "1") { name } }`))

	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body, "GraphQL introspection is not allowed")
	assert.Contains(t, body, `Cannot query field \"usr\" on type \"Query\".`)
	assert.NotContains(t, body, "Did you mean")
}
