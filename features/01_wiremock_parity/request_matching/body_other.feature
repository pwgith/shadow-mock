@wiremock @FR-2.5
Feature: Plain, binary and multipart body matching

  Background:
    Given Shadow Mock is running

  @P0
  Scenario Outline: Plain-text body matchers
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/t", "bodyPatterns": [ <pattern> ] }, "response": { "status": 200 } }
      """
    When I send a "POST" request to "/t" with body:
      """
      hello world
      """
    Then the response status is <status>

    Examples:
      | pattern                                                     | status |
      | { "equalTo": "hello world" }                                 | 200    |
      | { "equalTo": "HELLO WORLD", "caseInsensitive": true }        | 200    |
      | { "contains": "world" }                                      | 200    |
      | { "doesNotContain": "world" }                                | 404    |
      | { "matches": "^hello .*$" }                                  | 200    |
      | { "absent": true }                                           | 404    |

  @P3
  Scenario: binaryEqualTo compares base64-decoded bytes
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/bin", "bodyPatterns": [ { "binaryEqualTo": "AQID" } ] }, "response": { "status": 200 } }
      """
    When I send a "POST" request to "/bin" with the bytes "01 02 03"
    Then the response status is 200

  @P3
  Scenario Outline: Multipart patterns with ANY and ALL matching
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/upload",
          "multipartPatterns": [ { "matchingType": "<type>",
              "headers": { "Content-Disposition": { "contains": "name=\"doc\"" } },
              "bodyPatterns": [ { "contains": "PDF" } ] } ] },
        "response": { "status": 200 } }
      """
    When I send a multipart "POST" request to "/upload" with parts:
      | name | content        |
      | doc  | %PDF-1.7 bytes |
      | meta | {"a":1}        |
    Then the response status is <status>

    Examples:
      | type | status |
      | ANY  | 200    |
      | ALL  | 404    |
