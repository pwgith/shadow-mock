@wiremock @P3 @FR-10.16
Feature: Replacements for WireMock's jwt, faker and graphql extensions
  WireMock publishes JWT template helpers, a Faker helper and a GraphQL request matcher as separate
  extensions. Shadow Mock has its own, off until switched on with templating.extensions or
  matching.extensions (or WireMock's --extensions with the extension's Java class). They were written from
  the extensions' published sources. The tokens, key sets and values themselves are random or depend on keys,
  so the unit tests (internal/respond/template, internal/graphql) check their shape and signatures.

  Scenario: The graphql matcher compares queries without regard to layout or order
    Given Shadow Mock is running with configuration:
      """yaml
      matching:
        extensions: [graphql]
      """
    And the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/graphql",
                     "customMatcher": { "name": "graphql-body-matcher",
                                        "parameters": { "query": "query Q($id: ID!) { user(id: $id) { name email } }",
                                                        "variables": { "id": 7 }, "operationName": "Q" } } },
        "response": { "status": 200, "body": "found" } }
      """
    When I send a "POST" request to "/graphql" with body:
      """json
      { "operationName": "Q", "variables": { "id": 7 },
        "query": "query Q($id: ID!) {\n  user(id: $id) {\n    email\n    name\n  }\n}" }
      """
    Then the response status is 200
    And the response body equals 'found'

  Scenario: The graphql matcher turns away another query, other variables or another operation name
    Given Shadow Mock is running with configuration:
      """yaml
      matching:
        extensions: [graphql]
      """
    And the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/graphql",
                     "customMatcher": { "name": "graphql-body-matcher",
                                        "parameters": { "query": "{ user { name } }", "variables": { "id": 7 } } } },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/graphql" with body:
      """json
      { "query": "{ user { email } }", "variables": { "id": 7 } }
      """
    Then the response status is 404
    When I send a "POST" request to "/graphql" with body:
      """json
      { "query": "{ user { name } }", "variables": { "id": 8 } }
      """
    Then the response status is 404
    When I send a "POST" request to "/graphql" with body:
      """json
      { "query": "{ user { name } }", "variables": { "id": 7 }, "operationName": "X" }
      """
    Then the response status is 404

  Scenario: The graphql matcher does not exist until it is switched on
    Given Shadow Mock is running
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/graphql", "customMatcher": { "name": "graphql-body-matcher", "parameters": { "query": "{ a }" } } },
        "response": { "status": 200 } }
      """
    Then the response status is 422
    And the response JSON at "$.errors[0].detail" contains "unknown custom matcher"

  Scenario: The jwt helper signs a token
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [jwt]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/token" },
        "response": { "status": 200, "body": "{{jwt sub='alice' maxAge='1 hours' role='admin'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/token"
    Then the response status is 200
    And the response body matches "^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$"

  Scenario: The jwks helper publishes the key that signs RS256 tokens
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [jwt]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/.well-known/jwks.json" },
        "response": { "status": 200, "body": "{{jwks}}", "headers": { "Content-Type": "application/json" }, "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/.well-known/jwks.json"
    Then the response JSON at "$.keys[0].kty" is "RSA"
    And the response JSON at "$.keys[0].alg" is "RS256"
    And the response JSON at "$.keys[0].use" is "sig"

  Scenario: A mistake in the jwt arguments is written into the response
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [jwt]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/token" },
        "response": { "status": 200, "body": "{{jwt maxAge='soon'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/token"
    Then the response body equals '[ERROR: maxAge must consist of two parts - amount and unit e.g. 12 days]'

  Scenario: The random helper makes fake values
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [faker]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/person" },
        "response": { "status": 200, "body": "{{random 'Name.firstName'}} <{{random 'Internet.emailAddress'}}> {{random 'Number.digits 4'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/person"
    Then the response body matches "^[A-Z][a-z]+ <[a-z]+\.[a-z]+@[a-z]+\.[a-z]+> [0-9]{4}$"

  Scenario: The random helper writes an error for an expression it cannot evaluate
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [faker]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/person" },
        "response": { "status": 200, "body": "{{random 'Nope.thing'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/person"
    Then the response body equals '[ERROR: Unable to evaluate the expression Nope.thing]'
