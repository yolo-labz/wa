package main

import "github.com/spf13/pflag"

// chatAliasFor returns a pflag normalization func that rewrites the flag
// name "chat" to the given canonical recipient-flag name (one of "to",
// "jid", or "group"), leaving every other flag name untouched.
//
// Audit finding #8: the recipient JID argument is spelled four ways across
// the CLI — `--to`, `--chat`, `--jid`, `--group`. Rather than rename any
// existing flag (which would break callers), we register `--chat` as a
// universal recipient alias on every command whose canonical recipient flag
// is `--to`/`--jid`/`--group`. Commands already on `--chat` need nothing.
//
// pflag applies the normalize func both when DEFINING a flag and when
// PARSING argv. So the canonical flag must already be defined under its real
// name before SetNormalizeFunc is installed — otherwise the definition of
// e.g. `--to` would itself be normalized. Callers therefore define all flags
// first, then call applyChatAlias last (see each command's init()).
func chatAliasFor(canonical string) func(*pflag.FlagSet, string) pflag.NormalizedName {
	return func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "chat" {
			return pflag.NormalizedName(canonical)
		}
		return pflag.NormalizedName(name)
	}
}

// applyChatAlias installs chatAliasFor(canonical) on cmd's flag set so that
// `--chat` is accepted as a synonym for the command's canonical recipient
// flag. Call this AFTER all of the command's flags have been defined.
func applyChatAlias(cmd interface{ Flags() *pflag.FlagSet }, canonical string) {
	cmd.Flags().SetNormalizeFunc(chatAliasFor(canonical))
}
