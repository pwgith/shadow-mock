@wiremock @P3 @FR-10.16 @FR-10.16a @FR-10.16b @FR-10.16c @FR-10.16d
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

  Scenario: The jwt helper's defaults and claims
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [jwt]
        jwt:
          hs256Secret: unit-test-secret
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/defaults" },
        "response": { "status": 200, "body": "{{jwt}}", "transformers": ["response-template"] } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/claims" },
        "response": { "status": 200, "transformers": ["response-template"],
                      "body": "{{jwt iss='me' aud='you' sub='alice' admin=true level=3 ratio=1.5 role='ops' groups=(claims 'a' 'b') ids=(claims 1 2) profile=(claimsObject name='x' n=2)}}" } }
      """
    When I send a "GET" request to "/defaults"
    Then the response body is a JWT signed with HS256 by the secret "unit-test-secret" and claims:
      | iss | wiremock    |
      | aud | wiremock.io |
      | sub | user-123    |
    When I send a "GET" request to "/claims"
    Then the response body is a JWT signed with HS256 by the secret "unit-test-secret" and claims:
      | iss     | me                 |
      | aud     | you                |
      | sub     | alice              |
      | admin   | true               |
      | level   | 3                  |
      | ratio   | 1.5                |
      | role    | ops                |
      | groups  | ["a","b"]          |
      | ids     | [1,2]              |
      | profile | {"n":2,"name":"x"} |

  Scenario Outline: A mistake in the jwt arguments is written into the response
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [jwt]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/token" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/token"
    Then the response body equals '<error>'

    Examples:
      | template                      | error                                                                 |
      | {{jwt maxAge='x days'}}       | [ERROR: maxAge amount must be a whole number]                         |
      | {{jwt maxAge='1 fortnights'}} | [ERROR: maxAge unit must be one of: seconds, minutes, hours, days]    |
      | {{jwt tags=(claims 'a' 1)}}   | [ERROR: items for array claim tags are not all the same type]         |
      | {{jwt tags=(claims 1.5 2.5)}} | [ERROR: items for array claim tags are not of type string or integer] |

  Scenario: An RS256 token verifies against the key set the jwks helper publishes
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [jwt]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/token" },
        "response": { "status": 200, "body": "{{jwt alg='RS256' sub='alice'}}", "transformers": ["response-template"] } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/jwks" },
        "response": { "status": 200, "body": "{{jwks}}", "headers": { "Content-Type": "application/json" }, "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/token"
    Then the response body is a JWT signed with RS256 by the key at "/jwks"
    And the response body is a JWT with claims:
      | sub | alice |

  Scenario Outline: The random helper makes values of the right shape
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        extensions: [faker]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/fake" },
        "response": { "status": 200, "body": "{{random \"<expression>\"}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/fake"
    Then the response body matches "<shape>"

    Examples:
      | expression                   | shape                                                          |
      | Name.firstName               | ^[A-Z][a-z]+$                                                  |
      | Name.first_name              | ^[A-Z][a-z]+$                                                  |
      | Name.fullName                | ^[^ ]+ [^ ]+$                                                  |
      | Address.city                 | ^[A-Z][a-z]+$                                                  |
      | Address.zipCode              | ^[0-9]{5}$                                                     |
      | Address.stateAbbr            | ^[A-Z]{2}$                                                     |
      | Address.fullAddress          | ^[0-9]+ .+, .+, [A-Z]{2} [0-9]{5}$                             |
      | Internet.emailAddress        | ^[a-z]+\.[a-z]+@[a-z]+\.[a-z]+$                                |
      | Internet.ipV4Address         | ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$                               |
      | Internet.uuid                | ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ |
      | Company.name                 | ^[^ ]+ .+$                                                     |
      | Lorem.words 5                | ^([a-z]+ ){4}[a-z]+$                                           |
      | Number.digits 6              | ^[0-9]{6}$                                                     |
      | Number.numberBetween '5','7' | ^[56]$                                                         |
      | PhoneNumber.cellPhone        | ^[0-9]{3}-[0-9]{3}-[0-9]{4}$                                   |
      | Color.hex                    | ^#[0-9a-f]{6}$                                                 |

  Scenario Outline: The graphql matcher sees through layout and order but not meaning
    Given Shadow Mock is running with configuration:
      """yaml
      matching:
        extensions: [graphql]
      """
    And the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/graphql",
                     "customMatcher": { "name": "graphql-body-matcher",
                                        "parameters": { "query": "query Q { user(id: 1, active: true) { name email ... on Admin { level } } }" } } },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/graphql" with body:
      """json
      { "query": "<query>" }
      """
    Then the response status is <status>

    Examples:
      | query                                                                                  | status |
      | query Q { user(active: true, id: 1) { email name ... on Admin { level } } }             | 200    |
      | query Q { user(id: 1, active: true) { ... on Admin { level } name email } } # a comment | 200    |
      | query Q { user(id: 1, active: true) { name email ... on Admin { level } other } }       | 404    |
      | query Q { user(id: 2, active: true) { name email ... on Admin { level } } }             | 404    |
      | query R { user(id: 1, active: true) { name email ... on Admin { level } } }             | 404    |
      | query Q { user(id: 1, active: true) { name email ... on Guest { level } } }             | 404    |
      | query Q { user(id: 1, active: true) { name email                                        | 404    |

  Scenario Outline: Extension settings that cannot work are refused at start-up
    Given the configuration file contains:
      """yaml
      <settings>
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "<mentions>"

    Examples:
      | settings                                                      | mentions                             |
      | templating: { extensions: [nope] }                            | templating.extensions                |
      | templating: { extensions: [graphql] }                         | templating.extensions                |
      | matching: { extensions: [jwt] }                               | matching.extensions                  |
      | templating: { jwt: { hs256Secret: s } }                       | needs jwt in templating.extensions   |
      | templating: { extensions: [jwt], jwt: { rs256PublicKey: p } } | needs templating.jwt.rs256PrivateKey |

  Scenario: A JWT private key that is not usable stops start-up
    Then starting Shadow Mock with configuration fails with an error mentioning "templating.jwt.rs256PrivateKey":
      """yaml
      templating:
        extensions: [jwt]
        jwt: { rs256PrivateKey: not-a-key }
      """
