@wiremock @P3 @standalone @FR-10.11
Feature: Stub CORS and files served from __files
  Behaviours WireMock's server has that are not about one mapping: CORS for browser clients, and the
  files in __files served over HTTP. Every expectation below was checked against a real WireMock 3.13.2.

  Scenario: CORS is off by default
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/s" }, "response": { "status": 200, "body": "s" } }
      """
    When I send a request:
      | method  | path | header:Origin         | header:Access-Control-Request-Method |
      | OPTIONS | /s   | https://a.example     | POST                                 |
    Then the response body equals 's'
    And the response header "Access-Control-Allow-Origin" is absent

  Rule: With server.stubCors on

    Background:
      Given Shadow Mock is running with configuration:
        """yaml
        server:
          stubCors: true
        """
      And the global mapping:
        """json
        { "request": { "urlPath": "/s" }, "response": { "status": 200, "body": "s" } }
        """
      And the global mapping:
        """json
        { "request": { "urlPath": "/own" },
          "response": { "status": 200, "body": "o", "headers": { "Access-Control-Allow-Origin": "https://mine.example" } } }
        """

    Scenario: A preflight request is answered without a mapping
      When I send a request:
        | method  | path     | header:Origin     | header:Access-Control-Request-Method | header:Access-Control-Request-Headers |
        | OPTIONS | /nothing | https://a.example | POST                                 | X-Custom, Content-Type                |
      Then the response status is 200
      And the response body equals ''
      And the response header "Access-Control-Allow-Origin" is "https://a.example"
      And the response header "Access-Control-Allow-Credentials" is "true"
      And the response header "Access-Control-Allow-Methods" is "OPTIONS,GET,POST,PUT,PATCH,DELETE"
      And the response header "Access-Control-Allow-Headers" is "X-Custom,Content-Type"
      And the response header "Access-Control-Max-Age" is "1800"
      And the response header "Vary" is "Origin"

    Scenario: A preflight request is not mock traffic
      When I send a request:
        | method  | path | header:Origin     | header:Access-Control-Request-Method |
        | OPTIONS | /s   | https://a.example | GET                                  |
      And I call admin "GET" "/requests"
      Then the response JSON at "$.requests" has 0 items

    Scenario Outline: A request with an Origin gets the headers, and one without gets only Vary
      When I send a request:
        | method | path | header:Origin |
        | GET    | /s   | <origin>      |
      Then the response body equals 's'
      And the response header "Access-Control-Allow-Origin" is "<allowed>"

      Examples:
        | origin            | allowed           |
        | https://a.example | https://a.example |
        | null              | null              |

    Scenario: A request without an Origin is not given CORS headers
      When I send a "GET" request to "/s"
      Then the response header "Vary" is "Origin"
      And the response header "Access-Control-Allow-Origin" is absent

    Scenario: An OPTIONS request that is not a preflight reaches the mapping
      When I send a request:
        | method  | path | header:Origin     |
        | OPTIONS | /s   | https://a.example |
      Then the response body equals 's'
      And the response header "Access-Control-Allow-Origin" is "https://a.example"

    Scenario: A header the mapping sets for itself is kept
      When I send a request:
        | method | path | header:Origin     |
        | GET    | /own | https://a.example |
      Then the response header "Access-Control-Allow-Origin" is "https://mine.example"
      And the response header "Access-Control-Allow-Credentials" is "true"

  Rule: Files in __files are served over HTTP

    Background:
      Given Shadow Mock is running
      And the file "__files/hello.txt":
        """
        hello file
        """
      And the file "__files/data.json":
        """
        {"a":1}
        """
      And the file "__files/sub/deep.txt":
        """
        deep
        """
      And the global mapping:
        """json
        { "request": { "urlPath": "/hello.txt" }, "response": { "status": 200, "body": "from the mapping" } }
        """

    Scenario Outline: A file is served at /__files/ and, when no mapping matches, at its own path
      When I send a "GET" request to "<path>"
      Then the response status is 200
      And the response body equals '<body>'
      And the response header "Content-Type" contains "<type>"

      Examples:
        | path                  | body        | type             |
        | /__files/hello.txt    | hello file  | text/plain       |
        | /__files/data.json    | {"a":1}     | application/json |
        | /__files/sub/deep.txt | deep        | text/plain       |
        | /sub/deep.txt         | deep        | text/plain       |
        | /data.json            | {"a":1}     | application/json |

    Scenario: A mapping wins over a file at the file's own path, but not under /__files/
      When I send a "GET" request to "/hello.txt"
      Then the response body equals 'from the mapping'
      When I send a "GET" request to "/__files/hello.txt"
      Then the response body equals 'hello file'

    Scenario Outline: What is not a file is not served
      When I send a "GET" request to "<path>"
      Then the response status is 404

      Examples:
        | path                           |
        | /__files/missing.txt           |
        | /__files/../mappings/x.json    |
        | /__files/%2e%2e/secret.txt     |
        | /__files/sub/..%2f..%2fx       |
        | /missing.txt                   |

    Scenario: A folder is refused, not listed
      When I send a "GET" request to "/__files/sub"
      Then the response status is 403

    Scenario: A file is not mock traffic
      When I send a "GET" request to "/__files/hello.txt"
      And I call admin "GET" "/requests"
      Then the response JSON at "$.requests" has 0 items

  Rule: Serving files can be turned off

    Scenario: server.serveFiles is false
      Given Shadow Mock is running with configuration:
        """yaml
        server:
          serveFiles: false
        """
      And the file "__files/hello.txt":
        """
        hello file
        """
      When I send a "GET" request to "/__files/hello.txt"
      Then the response status is 404
      When I send a "GET" request to "/hello.txt"
      Then the response status is 404
