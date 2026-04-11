self: {
  config,
  lib,
  pkgs,
  ...
}:
with lib; let
  cfg = config.services.wa;
  waPackage = self.packages.${pkgs.system}.default;
in {
  options.services.wa = {
    enable = mkEnableOption "wa user daemon (WhatsApp automation)";

    package = mkOption {
      type = types.package;
      default = waPackage;
      defaultText = literalExpression "wa.packages.\${pkgs.system}.default";
      description = "The wa package to install into the user profile.";
    };

    profile = mkOption {
      type = types.str;
      default = "default";
      example = "work";
      description = ''
        Profile name passed as --profile to wad. Must match the regex
        ^[a-z][a-z0-9-]{0,30}[a-z0-9]$ and must not be a reserved
        name. See specs/008-multi-profile/spec.md FR-002/FR-003.

        Multiple profiles on one user account: import this module
        once per profile with different names. Each import creates a
        distinct wad@<profile>.service user unit.
      '';
    };

    logLevel = mkOption {
      type = types.enum ["debug" "info" "warn" "error"];
      default = "info";
      description = "slog level passed to wad via WA_LOG_LEVEL.";
    };

    installCli = mkOption {
      type = types.bool;
      default = true;
      description = "Install the wa CLI into home.packages (lets users run `wa profile list`, etc.).";
    };

    autoStart = mkOption {
      type = types.bool;
      default = true;
      description = ''
        Start the wad systemd user service at login (via
        wantedBy = [ "default.target" ]). Disable if you want to
        start it manually with `systemctl --user start wad@<profile>`.

        NOTE: user services only survive logout if `loginctl
        enable-linger $USER` has been run. home-manager does not
        manage linger; you must run it yourself once per user.
      '';
    };

    extraArgs = mkOption {
      type = types.listOf types.str;
      default = [];
      example = ["--log-level=debug"];
      description = "Additional flags passed to wad after --profile.";
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = builtins.match "^[a-z][a-z0-9-]{0,30}[a-z0-9]$" cfg.profile != null;
        message = "services.wa.profile ${cfg.profile} does not match ^[a-z][a-z0-9-]{0,30}[a-z0-9]$ (see FR-002).";
      }
    ];

    home.packages = lib.optional cfg.installCli cfg.package;

    # systemd user service — mirrors the template unit in
    # specs/008-multi-profile/contracts/service-templates.md but
    # materialised as a real unit via home-manager's
    # systemd.user.services output.
    systemd.user.services."wad@${cfg.profile}" = {
      Unit = {
        Description = "wa daemon (${cfg.profile} profile, user)";
        Documentation = ["https://github.com/yolo-labz/wa"];
        After = ["network-online.target"];
        Wants = ["network-online.target"];
      };

      Service = {
        Type = "simple";
        ExecStart = "${cfg.package}/bin/wad --profile ${cfg.profile} ${lib.escapeShellArgs cfg.extraArgs}";
        Restart = "on-failure";
        RestartSec = "5s";
        Environment = [
          "WA_LOG_LEVEL=${cfg.logLevel}"
          "GOTRACEBACK=crash"
        ];
        StandardOutput = "journal";
        StandardError = "journal";

        # User-mode sandboxing subset per research.md D10 and
        # specs/008-multi-profile/contracts/service-templates.md.
        # These are the directives that actually take effect in user
        # units (unlike the system unit which gets the full set).
        NoNewPrivileges = true;
        LockPersonality = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        SystemCallFilter = "@system-service";
        SystemCallArchitectures = "native";

        # INTENTIONALLY ABSENT (see research.md D10 for the full
        # rationale):
        #
        #   ProtectSystem, ProtectHome, PrivateTmp, PrivateDevices,
        #   RestrictNamespaces — require mount namespace + CAP_SYS_ADMIN
        #   which the user manager does not have.
        #
        #   MemoryDenyWriteExecute — incompatible with Go runtime
        #   (systemd#3814). Enabling it segfaults wad at startup.
      };

      Install = lib.mkIf cfg.autoStart {
        WantedBy = ["default.target"];
      };
    };
  };
}
