@standalone @P0 @NFR-6.2 @NFR-6.1
Feature: Configuration from file, environment and flags
  Configuration comes from a YAML file, environment variables and flags.
  Flags override environment variables, which override the file.

  Scenario Outline: Precedence of configuration sources
    Given the configuration file sets "listen.mock" to ":7001"
    And the environment variable "SHADOWMOCK_LISTEN_MOCK" is "<env>"
    And the command-line flag "--listen-mock" is "<flag>"
    When Shadow Mock starts
    Then it listens for mock traffic on "<effective>"

    Examples:
      | env   | flag  | effective |
      |       |       | :7001     |
      | :7002 |       | :7002     |
      | :7002 | :7003 | :7003     |

  Scenario: Unknown configuration keys fail fast
    Given the configuration file contains:
      """yaml
      sesions: { defaultTtl: 1h }
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "sesions"

  Scenario: Invalid values fail fast with a clear message
    Given the configuration file contains:
      """yaml
      sessions: { detect: { ambiguity: sometimes } }
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "ambiguity must be one of first, reject, global"

  # Needs Docker and the image build (P4).
  @wip
  Scenario: The engine runs from the Docker image with defaults
    When I run the Docker image "shadowmock:latest" with port 8080 published
    Then "GET /__health" returns 200
    And a "GET" request to "/anything" returns status 404
