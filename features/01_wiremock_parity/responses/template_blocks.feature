@wiremock @P3 @FR-10.3
Feature: Template block helpers and variables
  Some helpers take the text between their tags as input, and some set a variable that the rest
  of the template reads. Every expectation below was checked against a real WireMock 3.13.2.

  Background:
    Given Shadow Mock is running

  Scenario Outline: Helpers that set and read variables
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                                 | body     |
      | {{#assign 'x'}}hi{{/assign}}[{{x}}]                      | [hi]     |
      | {{#assign 'x'}}  padded  {{/assign}}[{{x}}]              | [padded] |
      | {{#assign 'x'}}{{capitalize 'term'}}{{/assign}}{{x}}     | Term     |
      | [{{#assign 'x'}}hi{{/assign}}]                           | []       |
      | {{#parseJson 'o'}}{\"name\":\"n\"}{{/parseJson}}{{o.name}} | n        |
      | [{{val 10 assign='v'}}]{{#lt v 20}}Less{{else}}More{{/lt}} | []Less   |
      | {{val missing or='dflt' assign='v'}}{{v}}                | dflt     |

  Scenario Outline: Helpers that read the text between their tags
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                                                           | body                     |
      | [{{#trim}}  x  {{/trim}}]                                                           | [x]                      |
      | {{#formatJson format='compact'}} { \"a\" : 1, \"b\" : [ 1, 2 ] } {{/formatJson}}    | {"a":1,"b":[1,2]}        |
      | {{#formatXml format='compact'}} <foo> <bar>wh</bar> </foo> {{/formatXml}}           | <foo><bar>wh</bar></foo> |

  Scenario: formatJson lays JSON out the way WireMock does
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "transformers": ["response-template"],
          "body": "{{#formatJson}}{\"foo\":true,\"bar\":{\"baz\":false},\"l\":[1,2,{\"a\":1}],\"e\":[],\"o\":{}}{{/formatJson}}" } }
      """
    When I send a "GET" request to "/h"
    Then the response body is:
      """
      {
        "foo" : true,
        "bar" : {
          "baz" : false
        },
        "l" : [ 1, 2, {
          "a" : 1
        } ],
        "e" : [ ],
        "o" : { }
      }
      """

  Scenario: formatXml indents elements and keeps comments and attributes
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "transformers": ["response-template"],
          "body": "{{#formatXml}}<foo a=\"1\"><bar >wh</bar><e/><!-- c --><x><y>1</y></x></foo >{{/formatXml}}" } }
      """
    When I send a "GET" request to "/h"
    Then the response body is:
      """
      <foo a="1">
        <bar>wh</bar>
        <e/>
        <!-- c -->
        <x>
          <y>1</y>
        </x>
      </foo>
      """

  Scenario Outline: Building JSON from variables
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                                                                                                              | body                      |
      | {{#assign 'a'}}[1]{{/assign}}{{#jsonArrayAdd a}}2{{/jsonArrayAdd}}                                                                     | [1,2]                     |
      | {{#jsonArrayAdd (val missing or='[]')}}{\"id\":321}{{/jsonArrayAdd}}                                                                   | [{"id":321}]              |
      | {{#assign 'a'}}[1,2]{{/assign}}{{#jsonArrayAdd a maxItems=2}}3{{/jsonArrayAdd}}                                                        | [2,3]                     |
      | {{#assign 'a'}}[1]{{/assign}}{{#jsonArrayAdd a}}[2]{{/jsonArrayAdd}}                                                                   | [1,[2]]                   |
      | {{#assign 'a'}}[1]{{/assign}}{{#jsonArrayAdd a flatten=true}}[2,3]{{/jsonArrayAdd}}                                                    | [1,2,3]                   |
      | {{#assign 'o'}}{\"a\":1,\"b\":{\"c\":1}}{{/assign}}{{#jsonMerge o}}{\"b\":{\"d\":2}}{{/jsonMerge}}                                     | {"a":1,"b":{"c":1,"d":2}} |
      | {{#assign 'o'}}{\"keepMe\":1,\"removeMe\":2}{{/assign}}{{#jsonMerge o removeNulls=true}}{\"removeMe\":null}{{/jsonMerge}}             | {"keepMe":1}              |
      | {{#assign 'o'}}{\"a\":1,\"b\":2}{{/assign}}{{jsonRemove o '$.a'}}                                                                      | {"b":2}                   |

  Scenario Outline: Comparison and test helpers also work as blocks
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                     | body     |
      | {{#eq 'a' 'a'}}eq{{/eq}}                      | eq       |
      | {{#neq 1 1}}x{{else}}neq-else{{/neq}}         | neq-else |
      | {{#gt 3 2}}gt{{/gt}}                          | gt       |
      | {{#and true false}}x{{else}}and-else{{/and}}  | and-else |
      | {{#or false true}}or{{/or}}                   | or       |
      | {{#not false}}not{{/not}}                     | not      |
      | {{#matches '123' '[0-9]+'}}YES{{/matches}}    | YES      |
      | {{#contains 'abcde' 'abc'}}YES{{/contains}}   | YES      |

  Scenario: A block helper given text that is not valid input fails the response
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{#parseJson 'x'}}not json{{/parseJson}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response status is 500
