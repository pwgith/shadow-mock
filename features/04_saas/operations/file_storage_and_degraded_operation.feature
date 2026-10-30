@saas @P5 @FR-11.13 @FR-11.14 @NFR-3.1
Feature: Files in an object store, and what happens when a dependency is down
  The management app uploads file content to an object store and records its name, size and SHA-256 in the
  database. The engine fetches a file on first use with a read-only credential, checks the hash and keeps a bounded
  cache.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And a file "payload.json" with content "{\"ok\":true}" stored for org "acme"
    And a mapping "file" in the global layer for "GET /file" whose response has "bodyFileName": "payload.json"

  Scenario: A file is fetched on first use and then cached
    When the SUT sends "GET /acme/file" twice
    Then both responses have the body "{\"ok\":true}"
    And the object store was read once

  Scenario: Content is addressed by its hash under the org's own prefix
    Then the object for "payload.json" is at "files/<org id>/<sha256 of the content>"
    And the key was built from the org and the hash, not from the file's name

  Scenario: The engine checks the hash
    Given the object's content no longer matches its recorded SHA-256
    When the SUT sends "GET /acme/file"
    Then the response status is 500
    And the engine logs a hash mismatch
    And the bad content is not cached

  Scenario: The engine's credential cannot write
    Then the object store credential the engine uses is read-only

  Scenario: The cache is bounded
    Given the cache holds 10 MB and the org has 50 files of 1 MB
    When the SUT requests all 50 files
    Then the cache never holds more than 10 MB
    And every request is served

  Scenario: A slow or unavailable store serves what is cached
    Given "payload.json" is cached
    And the object store becomes unreachable
    When the SUT sends "GET /acme/file"
    Then the response status is 200

  Scenario: An uncached file says why it cannot be served
    Given the object store becomes unreachable
    And "payload.json" is not cached
    When the SUT sends "GET /acme/file"
    Then the response status is 503
    And the response header "X-Shadow-Mock-Reason" is "object_store_unavailable"

  Scenario: A database outage keeps serving the last configuration
    Given the database becomes unreachable
    When the SUT sends "GET /acme/file"
    Then the response status is 200
    And readiness reports "degraded"

  Scenario: Journal bodies over 64 KiB go to the store and are capped
    Given journal bodies are stored for the org
    When the SUT sends a request with a 100 KiB body
    Then the journal entry's body is in the object store under "journal/<org id>/"
    And a body over 256 KiB is stored truncated

  Scenario: Journal bodies expire with the retention period
    Given the plan keeps journal bodies for 30 days
    When 31 days pass
    Then the bodies are no longer readable

  Scenario: A file name belongs to the org only
    Given the org "globex" has a file "payload.json" with different content
    When the SUT sends "GET /globex/file"
    Then the response has globex's content
