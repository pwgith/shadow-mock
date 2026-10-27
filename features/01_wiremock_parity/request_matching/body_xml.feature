@wiremock @P3 @FR-2.5
Feature: XML body matching

  Background:
    Given Shadow Mock is running

  Scenario Outline: equalToXml ignores formatting but not content
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/x",
          "bodyPatterns": [ { "equalToXml": "<claim><id>1</id><status>OPEN</status></claim>" } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/x" with body:
      """xml
      <body>
      """
    Then the response status is <code>

    Examples:
      | body                                                      | code |
      | <claim>  <id>1</id>  <status>OPEN</status>  </claim>      | 200    |
      | <claim><id>1</id><status>CLOSED</status></claim>          | 404    |

  Scenario: equalToXml with placeholders
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/xp",
          "bodyPatterns": [ { "equalToXml": "<claim><id>${xmlunit.ignore}</id></claim>", "enablePlaceholders": true } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/xp" with body:
      """xml
      <claim><id>987</id></claim>
      """
    Then the response status is 200

  Scenario Outline: matchesXPath with and without sub-matchers
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/xpath", "bodyPatterns": [ <pattern> ] }, "response": { "status": 200 } }
      """
    When I send a "POST" request to "/xpath" with body:
      """xml
      <claim><id>42</id><items><item sku="A1"/></items></claim>
      """
    Then the response status is <status>

    Examples:
      | pattern                                                                       | status |
      | { "matchesXPath": "/claim/id" }                                                | 200    |
      | { "matchesXPath": "/claim/missing" }                                           | 404    |
      | { "matchesXPath": "//item[@sku='A1']" }                                        | 200    |
      | { "matchesXPath": { "expression": "/claim/id/text()", "equalTo": "42" } }      | 200    |

  Scenario: matchesXPath with namespaces
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/soap",
          "bodyPatterns": [ { "matchesXPath": "/soap:Envelope/soap:Body/c:GetClaim",
                              "xPathNamespaces": { "soap": "http://schemas.xmlsoap.org/soap/envelope/", "c": "urn:claims" } } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/soap" with body:
      """xml
      <soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><c:GetClaim xmlns:c="urn:claims"/></soap:Body></soap:Envelope>
      """
    Then the response status is 200
