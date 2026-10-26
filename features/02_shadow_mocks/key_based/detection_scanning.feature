@key-based @P1 @FR-7.4 @NFR-2.2 @NFR-2.3
Feature: Session detection by Aho-Corasick scanning
  When the system under test does not forward the key in a designated field,
  stage 4 scans every header value, the raw query string and the URL path,
  and stage 5 scans the request body (bounded, and only for allowed content
  types). The first stage with a hit wins.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200, "body": "global" } }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200, "body": "shadow" } }
      """

  Scenario Outline: Stage 4 finds the key anywhere in request metadata
    When I send a request:
      | method | path   | <field>   |
      | GET    | <path> | <value>   |
    Then the response body is "shadow"
    And the journal entry records detection stage "ScanMeta"

    Examples:
      | field              | path                          | value                              |
      | header:traceparent | /a                            | 00-${S1.key}-01                    |
      | header:baggage     | /a                            | test.session=${S1.key},other=1     |
      | header:X-Corr-Id   | /a                            | corr-${S1.key}                     |
      | header:Accept      | /orders/${S1.key}/items       | */*                                |
      | header:Accept      | /a?ref=${S1.key}&x=1          | */*                                |

  Scenario Outline: Stage 5 finds the key in the request body
    When I send a request:
      | method | path | header:Content-Type | body   |
      | POST   | /b   | <type>              | <body> |
    Then the response body is "<result>"

    Examples:
      | type                              | body                                               | result |
      | application/json                  | {"meta":{"testRun":"${S1.key}"}}                   | shadow |
      | application/xml                   | <req><run>${S1.key}</run></req>                     | shadow |
      | application/x-www-form-urlencoded | a=1&run=${S1.key}                                  | shadow |
      | text/plain                        | run ${S1.key} please                               | shadow |
      | application/octet-stream          | ${S1.key}                                          | global |
      | image/png                         | ${S1.key}                                          | global |

  Scenario: Only the first maxScanBytes of the body are scanned
    Given Shadow Mock is running with configuration:
      """yaml
      sessions: { detect: { maxScanBytes: 1024 } }
      """
    When I send a "POST" request to "/b" with a JSON body where "${S1.key}" starts at byte 2000
    Then the response body is "global"
    And the journal entry records "scanTruncated" as true

  Scenario: Gzip-compressed bodies are decompressed before scanning
    When I send a "POST" request to "/b" with a gzip-compressed JSON body containing "${S1.key}"
    Then the response body is "shadow"

  Scenario: Body scanning can be turned off
    Given Shadow Mock is running with configuration:
      """yaml
      sessions: { detect: { scanBody: false } }
      """
    When I send a "POST" request to "/b" with body:
      """json
      { "run": "${S1.key}" }
      """
    Then the response body is "global"

  Scenario: A designated header hit skips scanning
    When I send a request:
      | method | path | header:X-Shadow-Mock-Key | body                 |
      | POST   | /b   | ${S1.key}                | {"x":"irrelevant"}   |
    Then the response body is "shadow"
    And the journal entry records detection stage "Header"

  Scenario: A metadata hit wins over a body hit for a different session
    Given a key-based shadow mock "S2"
    And the mapping in "S2":
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200, "body": "shadow-2" } }
      """
    When I send a request:
      | method | path | header:X-Corr-Id | body                    |
      | POST   | /b   | ${S1.key}        | {"run":"${S2.key}"}     |
    Then the response body is "shadow"

  Scenario: The body is still available unchanged to matching and proxying after scanning
    Given the mapping in "S1":
      """json
      { "priority": 1, "request": { "method": "POST", "urlPath": "/echo",
          "bodyPatterns": [ { "matchesJsonPath": { "expression": "$.amount", "equalTo": "10" } } ] },
        "response": { "status": 200, "body": "matched-after-scan" } }
      """
    When I send a "POST" request to "/echo" with body:
      """json
      { "amount": 10, "run": "${S1.key}" }
      """
    Then the response body is "matched-after-scan"

  Scenario: Session creation and deletion take effect within 100 ms
    Given a key-based shadow mock "S3" is created at time T
    Then a request with "${S3.key}" in its body is attributed to "S3" no later than T + 100 ms
    When "S3" is deleted at time T2
    Then a request with "${S3.key}" in its body is served by the global session no later than T2 + 100 ms
