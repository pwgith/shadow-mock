@nfr @wiremock @P3 @NFR-1.1 @NFR-1.2
Feature: WireMock compatibility

  # Needs the WireMock reference container and client libraries, which the in-process harness lacks;
  # the corpus conformance test and the live contract suite cover this ground in CI.
  @wip
  Scenario: The WireMock documentation corpus behaves identically
    Given the conformance corpus of mapping examples from the WireMock documentation
    When each example's requests are replayed against Shadow Mock and against the WireMock reference container
    Then status, headers and bodies are identical for every example not listed in "conformance/known-differences.md"

  # Needs the WireMock reference container and client libraries, which the in-process harness lacks;
  # the corpus conformance test and the live contract suite cover this ground in CI.
  @wip
  Scenario Outline: Official WireMock client libraries work unchanged
    Given the <client> WireMock client pointed at "{admin base}"
    When the client runs its standard stub, verify and reset test suite
    Then all tests pass

    Examples:
      | client          |
      | Java            |
      | .NET            |
      | JavaScript      |
      | Python          |

  # Needs the WireMock reference container and client libraries, which the in-process harness lacks;
  # the corpus conformance test and the live contract suite cover this ground in CI.
  @wip
  Scenario: Client libraries can be scoped to a session by base URL
    Given a key-based shadow mock "S1"
    And the Java WireMock client pointed at "{admin base}/sessions/${S1.id}"
    When the client runs its standard stub, verify and reset test suite
    Then all tests pass
    And the global session is unchanged

  @standalone
  # Needs the WireMock reference container and client libraries, which the in-process harness lacks;
  # the corpus conformance test and the live contract suite cover this ground in CI.
  @wip
  Scenario: Shadow Mock can replace a WireMock standalone container
    Given a project whose tests use "wiremock/wiremock" with a mounted mappings folder
    When the container image is replaced with "shadowmock" and the same mount
    Then the project's tests pass unchanged
