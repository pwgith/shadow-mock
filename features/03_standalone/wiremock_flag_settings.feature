@wiremock @P3 @standalone @FR-10.13
Feature: Each WireMock flag sets the configuration key that does the same
  Every flag in WireMock 3.13's --help is accepted on the command line, so a command or Docker command copied
  from a WireMock setup starts (internal/config/wmflags_test.go checks the whole list). A flag that does
  something sets a configuration key, which this feature reads back; what the key then does is described by the
  features of that setting. Flags that only tune the Java server are accepted with a warning and change nothing.

  Scenario Outline: A flag sets its key
    Given the command-line flags are "<flags>"
    When Shadow Mock starts
    Then the setting "<key>" is "<value>"

    Examples: templating, responses and the journal
      | flags                                       | key                                | value            |
      | --global-response-templating                | templating.global                  | true             |
      | --local-response-templating                 | templating.global                  | false            |
      | --disable-response-templating               | templating.disabled                | true             |
      | --permitted-system-keys HOME,PATH           | templating.permittedSystemKeys     | HOME,PATH        |
      | --enable-stub-cors                          | server.stubCors                    | true             |
      | --disable-gzip                              | respond.gzip                       | off              |
      | --no-request-journal                        | storage.journal.hot.maxEntries     | 0                |
      | --max-request-journal-entries 50            | storage.journal.hot.maxEntries     | 50               |
      | --logged-response-body-size-limit 100       | storage.journal.hot.maxBodyBytes   | 100              |
      | --filename-template {{id}}.json             | storage.global.filenameTemplate    | {{id}}.json      |
      | --bind-address 127.0.0.1                    | listen.bindAddress                 | 127.0.0.1        |
      | --disable-request-logging                   | log.requests                       | false            |
      | --print-all-network-traffic                 | log.requests                       | true             |
      | --admin-api-basic-auth ann:secret           | auth.driver                        | basic            |
      | --admin-api-basic-auth ann:secret           | auth.basic.username                | ann              |

    Examples: proxying and recording
      | flags                                       | key                                | value            |
      | --proxy-all http://up.example:9             | proxy.all                          | http://up.example:9 |
      | --proxy-all http://up.example:9 --record-mappings | proxy.record                 | true             |
      | --match-headers Accept,X-Tenant             | proxy.matchHeaders                 | Accept,X-Tenant  |
      | --preserve-host-header                      | proxy.preserveHostHeader           | true             |
      | --proxy-via proxy.example:3128              | proxy.via                          | proxy.example:3128 |
      | --proxy-timeout 5000                        | proxy.timeout                      | 5s               |
      | --allow-proxy-targets a.example,*.b.example | proxy.allowTargets                 | a.example,*.b.example |
      | --deny-proxy-targets 10.0.0.0/8             | proxy.denyTargets                  | 10.0.0.0/8       |
      | --proxy-pass-through false                  | proxy.passThrough                  | false            |

    Examples: HTTPS and HTTP/2
      | flags                                                                    | key                      | value      |
      | --https-port 8443                                                        | tls.port                 | 8443       |
      | --https-port 8443 --disable-http                                         | tls.disableHttp          | true       |
      | --https-port 8443 --https-keystore k.p12 --keystore-type PKCS12          | tls.keystoreType         | PKCS12     |
      | --https-port 8443 --https-keystore k.p12                                 | tls.keystore             | k.p12      |
      | --https-port 8443 --https-keystore k.jks --keystore-password pw          | tls.keystorePassword     | pw         |
      | --https-port 8443 --https-keystore k.jks --key-manager-password kp       | tls.keyManagerPassword   | kp         |
      | --https-port 8443 --https-truststore t.jks --truststore-type JKS         | tls.truststoreType       | JKS        |
      | --https-port 8443 --https-truststore t.jks --truststore-password tp      | tls.truststorePassword   | tp         |
      | --https-port 8443 --https-truststore t.jks --https-require-client-cert   | tls.requireClientCert    | true       |
      | --disable-http2-plain                                                    | tls.disableHttp2Plain    | true       |
      | --disable-http2-tls                                                      | tls.disableHttp2Tls      | true       |
      | --https-port 8443 --admin-api-require-https                              | tls.adminRequireHttps    | true       |

    Examples: browser proxying
      | flags                                                                          | key                              | value        |
      | --enable-browser-proxying                                                      | proxy.browser.enabled            | true         |
      | --enable-browser-proxying --ca-keystore ca.jks                                 | proxy.browser.caKeystore         | ca.jks       |
      | --enable-browser-proxying --ca-keystore ca.jks --ca-keystore-password pw       | proxy.browser.caKeystorePassword | pw           |
      | --enable-browser-proxying --ca-keystore ca.p12 --ca-keystore-type PKCS12       | proxy.browser.caKeystoreType     | PKCS12       |
      | --enable-browser-proxying --trust-all-proxy-targets                            | proxy.browser.trustAll           | true         |
      | --enable-browser-proxying --trust-proxy-target a.example --trust-proxy-target b.example | proxy.browser.trustTargets | a.example,b.example |

    Examples: extensions
      | flags                                                                              | key                    | value     |
      | --extensions org.wiremock.extension.jwt.JwtExtensionFactory,org.wiremock.RandomExtension | templating.extensions | jwt,faker |
      | --extensions io.github.nilwurtz.GraphqlBodyMatcher                                 | matching.extensions    | graphql   |

  Scenario Outline: Flags that tune the Java server are accepted and change nothing
    Given the command-line flags are "<flags>"
    When Shadow Mock starts
    Then it listens for mock traffic on ":8080"

    Examples:
      | flags                                   |
      | --container-threads 20                  |
      | --jetty-acceptor-threads 2              |
      | --async-response-enabled true           |
      | --max-http-client-connections 5         |
      | --disable-connection-reuse true         |
      | --disable-extensions-scanning           |
      | --disable-strict-http-headers true      |
      | --use-chunked-encoding never            |
      | --timeout 5000                          |
      | --webhook-threadpool-size 5             |

  Scenario Outline: Flags that need another flag say so
    Given the command-line flags are "<flags>"
    When Shadow Mock starts
    Then start-up fails with an error mentioning "<mentions>"

    Examples:
      | flags                                          | mentions                       |
      | --disable-http                                 | tls.port                       |
      | --admin-api-require-https                      | tls.port                       |
      | --https-keystore k.p12                         | tls.port                       |
      | --https-port 8443 --https-require-client-cert  | tls.truststore                 |
      | --ca-keystore ca.jks                           | proxy.browser.enabled          |
      | --trust-all-proxy-targets                      | proxy.browser.enabled          |
      | --ca-keystore-type BKS --enable-browser-proxying | caKeystoreType               |
      | --extensions org.wiremock.RandomExtension --enable-browser-proxying --proxy-via p:1 --trust-all-proxy-targets | proxy.via |
