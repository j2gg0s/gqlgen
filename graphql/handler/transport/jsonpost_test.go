package transport_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"
	"github.com/vektah/gqlparser"
	"github.com/vektah/gqlparser/ast"
)

func TestJsonPost(t *testing.T) {
	es := &graphql.ExecutableSchemaMock{
		QueryFunc: func(ctx context.Context, op *ast.OperationDefinition) *graphql.Response {
			return &graphql.Response{Data: []byte(`{"name":"test"}`)}
		},
		MutationFunc: func(ctx context.Context, op *ast.OperationDefinition) *graphql.Response {
			return graphql.ErrorResponse(ctx, "mutations are not supported")
		},
		SchemaFunc: func() *ast.Schema {
			return gqlparser.MustLoadSchema(&ast.Source{Input: `
				schema { query: Query }
				type Query {
					me: User!
					user(id: Int): User!
				}
				type User { name: String! }
			`})
		},
	}
	h := handler.New(es)
	h.AddTransport(transport.JsonPostTransport{})

	t.Run("success", func(t *testing.T) {
		resp := doRequest(h, "POST", "/graphql", `{"query":"{ me { name } }"}`)
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, `{"data":{"name":"test"}}`, resp.Body.String())
	})

	// Todo: Extract
	//t.Run("query caching", func(t *testing.T) {
	//	// Run enough unique queries to evict a bunch of them
	//	for i := 0; i < 2000; i++ {
	//		query := `{"query":"` + strings.Repeat(" ", i) + "{ me { name } }" + `"}`
	//		resp := doRequest(h, "POST", "/graphql", query)
	//		assert.Equal(t, http.StatusOK, resp.Code)
	//		assert.Equal(t, `{"data":{"name":"test"}}`, resp.Body.String())
	//	}
	//
	//	t.Run("evicted queries run", func(t *testing.T) {
	//		query := `{"query":"` + strings.Repeat(" ", 0) + "{ me { name } }" + `"}`
	//		resp := doRequest(h, "POST", "/graphql", query)
	//		assert.Equal(t, http.StatusOK, resp.Code)
	//		assert.Equal(t, `{"data":{"name":"test"}}`, resp.Body.String())
	//	})
	//
	//	t.Run("non-evicted queries run", func(t *testing.T) {
	//		query := `{"query":"` + strings.Repeat(" ", 1999) + "{ me { name } }" + `"}`
	//		resp := doRequest(h, "POST", "/graphql", query)
	//		assert.Equal(t, http.StatusOK, resp.Code)
	//		assert.Equal(t, `{"data":{"name":"test"}}`, resp.Body.String())
	//	})
	//})

	t.Run("decode failure", func(t *testing.T) {
		resp := doRequest(h, "POST", "/graphql", "notjson")
		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.Equal(t, resp.Header().Get("Content-Type"), "application/json")
		assert.Equal(t, `{"errors":[{"message":"json body could not be decoded: invalid character 'o' in literal null (expecting 'u')"}],"data":null}`, resp.Body.String())
	})

	t.Run("parse failure", func(t *testing.T) {
		resp := doRequest(h, "POST", "/graphql", `{"query": "!"}`)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
		assert.Equal(t, resp.Header().Get("Content-Type"), "application/json")
		assert.Equal(t, `{"errors":[{"message":"Unexpected !","locations":[{"line":1,"column":1}]}],"data":null}`, resp.Body.String())
	})

	t.Run("validation failure", func(t *testing.T) {
		resp := doRequest(h, "POST", "/graphql", `{"query": "{ me { title }}"}`)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
		assert.Equal(t, resp.Header().Get("Content-Type"), "application/json")
		assert.Equal(t, `{"errors":[{"message":"Cannot query field \"title\" on type \"User\".","locations":[{"line":1,"column":8}]}],"data":null}`, resp.Body.String())
	})

	t.Run("invalid variable", func(t *testing.T) {
		resp := doRequest(h, "POST", "/graphql", `{"query": "query($id:Int!){user(id:$id){name}}","variables":{"id":false}}`)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
		assert.Equal(t, resp.Header().Get("Content-Type"), "application/json")
		assert.Equal(t, `{"errors":[{"message":"cannot use bool as Int","path":["variable","id"]}],"data":null}`, resp.Body.String())
	})

	t.Run("execution failure", func(t *testing.T) {
		resp := doRequest(h, "POST", "/graphql", `{"query": "mutation { me { name } }"}`)
		assert.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, resp.Header().Get("Content-Type"), "application/json")
		assert.Equal(t, `{"errors":[{"message":"mutations are not supported"}],"data":null}`, resp.Body.String())
	})

	t.Run("validate content type", func(t *testing.T) {
		doReq := func(handler http.Handler, method string, target string, body string, contentType string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, target, strings.NewReader(body))
			if contentType != "" {
				r.Header.Set("Content-Type", contentType)
			}
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, r)
			return w
		}

		validContentTypes := []string{
			"application/json",
			"application/json; charset=utf-8",
		}

		for _, contentType := range validContentTypes {
			t.Run(fmt.Sprintf("allow for content type %s", contentType), func(t *testing.T) {
				resp := doReq(h, "POST", "/graphql", `{"query":"{ me { name } }"}`, contentType)
				assert.Equal(t, http.StatusOK, resp.Code)
				assert.Equal(t, `{"data":{"name":"test"}}`, resp.Body.String())
			})
		}

		invalidContentTypes := []string{
			"",
			"text/plain",

			// These content types are currently not supported, but they are supported by other GraphQL servers, like express-graphql.
			"application/x-www-form-urlencoded",
			"application/graphql",
		}

		for _, tc := range invalidContentTypes {
			t.Run(fmt.Sprintf("reject for content type %s", tc), func(t *testing.T) {
				resp := doReq(h, "POST", "/graphql", `{"query":"{ me { name } }"}`, tc)
				assert.Equal(t, http.StatusBadRequest, resp.Code)
				assert.Equal(t, fmt.Sprintf(`{"errors":[{"message":"%s"}],"data":null}`, "transport not supported"), resp.Body.String())
			})
		}
	})
}

func doRequest(handler http.Handler, method string, target string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)
	return w
}
