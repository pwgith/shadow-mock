@wiremock @P3 @standalone @FR-10.9 @FR-1.4
Feature: A mapping with a field Shadow Mock does not know is refused
  WireMock answers 422 with a JSON pointer when a mapping has a field it does not know, which
  catches a misspelt urlPath instead of a mapping that matches everything. mappings.unknownFields
  chooses between that and keeping the field (FR-1.4). Every expectation below was checked against
  a real WireMock 3.13.2.

  Scenario Outline: A misspelt field is a 422 that points at it
    Given Shadow Mock is running
    When I call admin "POST" "/mappings" with:
      """json
      <mapping>
      """
    Then the response status is 422
    And the response JSON at "$.errors[0].source.pointer" is "<pointer>"

    Examples:
      | mapping                                                                                                                                              | pointer                           |
      | { "request": { "urlPaht": "/a" }, "response": { "status": 200 } }                                                                                    | /request/urlPaht                  |
      | { "request": { "url": "/a" }, "response": { "statu": 200 } }                                                                                         | /response/statu                   |
      | { "reqest": { "url": "/a" }, "response": { "status": 200 } }                                                                                         | /reqest                           |
      | { "request": { "url": "/a" }, "response": { "status": 200 }, "prioriti": 1 }                                                                         | /prioriti                         |
      | { "request": { "url": "/a" }, "response": { "status": 200, "bodyFileNam": "x" } }                                                                    | /response/bodyFileNam             |
      | { "request": { "url": "/a" }, "response": { "delayDistribution": { "type": "lognormal", "median": 1, "sigma": 0.1, "medin": 1 } } }                  | /response/delayDistribution/medin |
      | { "request": { "url": "/a", "headers": { "X": { "equalz": "1" } } }, "response": {} }                                                                | /request/headers/X                |
      | { "request": { "url": "/a", "bodyPatterns": [ { "equalToJsn": "1" } ] }, "response": {} }                                                           | /request/bodyPatterns/0           |

  Scenario Outline: Fields that WireMock accepts anywhere are not refused
    Given Shadow Mock is running
    When I call admin "POST" "/mappings" with:
      """json
      <mapping>
      """
    Then the response status is 201

    Examples:
      | mapping                                                                                                                                  |
      | { "name": "n", "priority": 2, "persistent": false, "metadata": { "any": { "thing": 1 } }, "request": { "url": "/a" }, "response": {} }   |
      | { "request": { "url": "/a" }, "response": { "status": 200, "transformerParameters": { "anything": 1 } } }                                |
      | { "request": { "url": "/a" }, "response": {}, "serveEventListeners": [ { "name": "audit-log", "parameters": { "anything": "x" } } ] }         |
      | { "request": { "urlPath": "/a", "queryParameters": { "id": { "hasExactly": [ { "equalTo": "1" } ] } } }, "response": {} }               |
      | { "request": { "url": "/a" }, "response": {}, "callResponses": [ { "calls": "1", "response": { "status": 201 } } ] }                     |

  Scenario: An imported mapping with a misspelt field is refused with the position of the mapping
    Given Shadow Mock is running
    When I call admin "POST" "/mappings/import" with:
      """json
      { "mappings": [
          { "request": { "url": "/ok" }, "response": {} },
          { "request": { "urlPaht": "/a" }, "response": {} } ] }
      """
    Then the response status is 422
    And the response JSON at "$.errors[0].source.pointer" is "/mappings/1/request/urlPaht"

  Scenario: Keeping unknown fields is a setting
    Given Shadow Mock is running with configuration:
      """yaml
      mappings:
        unknownFields: keep
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "url": "/a", "vendorHint": 1 }, "response": { "status": 200 } }
      """
    Then the response status is 201
    And the response JSON at "$.request.vendorHint" is 1

  Scenario: A matcher without an operator is refused whatever the setting
    Given Shadow Mock is running with configuration:
      """yaml
      mappings:
        unknownFields: keep
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "url": "/a", "headers": { "X": { "equalz": "1" } } }, "response": {} }
      """
    Then the response status is 422
    And the response JSON at "$.errors[0].source.pointer" is "/request/headers/X"
