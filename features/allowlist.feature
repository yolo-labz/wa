Feature: Allowlist authorization

  The allowlist is the first brake described in CLAUDE.md §Safety: WhatsApp bans
  aggressive automation in hours, so nothing reaches a contact unless someone
  granted that exact action to that exact contact. It is default-deny and
  tiered — a contact allowed to read is not thereby allowed to send.

  Scenario: An ungranted contact is refused
    Given an empty allowlist
    When the allowlist is asked whether "5511987654321@s.whatsapp.net" may "send"
    Then the answer is refused

  Scenario: A grant permits only the action it names
    Given an empty allowlist
    And "5511987654321@s.whatsapp.net" is granted "read"
    When the allowlist is asked whether "5511987654321@s.whatsapp.net" may "send"
    Then the answer is refused

  Scenario: A granted action is permitted
    Given an empty allowlist
    And "5511987654321@s.whatsapp.net" is granted "read"
    When the allowlist is asked whether "5511987654321@s.whatsapp.net" may "read"
    Then the answer is permitted

  Scenario: A grant reaches only the contact it names
    Given an empty allowlist
    And "5511987654321@s.whatsapp.net" is granted "send"
    When the allowlist is asked whether "5521998877665@s.whatsapp.net" may "send"
    Then the answer is refused

  Scenario: Revoking one action leaves the others granted
    Given an empty allowlist
    And "5511987654321@s.whatsapp.net" is granted "read" and "send"
    When "send" is revoked from "5511987654321@s.whatsapp.net"
    Then "5511987654321@s.whatsapp.net" may "read"
    And "5511987654321@s.whatsapp.net" may not "send"

  Scenario: Revoking the last action drops the contact from the allowlist
    Given an empty allowlist
    And "5511987654321@s.whatsapp.net" is granted "read"
    When "read" is revoked from "5511987654321@s.whatsapp.net"
    Then the allowlist holds 0 contacts

  Scenario Outline: Every action is default-deny for an unknown contact
    Given an empty allowlist
    When the allowlist is asked whether "5511987654321@s.whatsapp.net" may "<action>"
    Then the answer is refused

    Examples:
      | action       |
      | read         |
      | send         |
      | group.add    |
      | group.create |
