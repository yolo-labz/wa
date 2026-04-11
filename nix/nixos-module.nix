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
    enable = mkEnableOption "wa daemon (WhatsApp automation)";

    package = mkOption {
      type = types.package;
      default = waPackage;
      defaultText = literalExpression "wa.packages.\${pkgs.system}.default";
      description = "The wa package to install. Defaults to the flake's default output.";
    };

    user = mkOption {
      type = types.str;
      default = "wa";
      description = ''
        System user that runs the wad daemon. A dedicated non-root
        user per the project's "never-as-root" safety rule.
      '';
    };

    group = mkOption {
      type = types.str;
      default = "wa";
      description = "System group for the wa user.";
    };

    profile = mkOption {
      type = types.str;
      default = "default";
      description = ''
        Profile name passed as --profile to wad. Must match the regex
        ^[a-z][a-z0-9-]{0,30}[a-z0-9]$ and must not be a reserved
        name. See specs/008-multi-profile/spec.md FR-002/FR-003.

        Multiple profiles on one host: declare `services.wa` multiple
        times by wrapping each with a module that overrides `profile`
        and `user` (or use the home-manager module for per-user
        installs).
      '';
    };

    dataDir = mkOption {
      type = types.path;
      default = "/var/lib/wa";
      description = ''
        XDG_DATA_HOME equivalent for the daemon. Holds session.db,
        messages.db, and per-profile state under \$dataDir/wa/\$profile/.
      '';
    };

    configDir = mkOption {
      type = types.path;
      default = "/var/lib/wa/config";
      description = "XDG_CONFIG_HOME equivalent. Holds allowlist.toml and active-profile pointer.";
    };

    stateDir = mkOption {
      type = types.path;
      default = "/var/lib/wa/state";
      description = "XDG_STATE_HOME equivalent. Holds audit.log and wad.log.";
    };

    logLevel = mkOption {
      type = types.enum ["debug" "info" "warn" "error"];
      default = "info";
      description = "slog level passed to wad via --log-level.";
    };

    openFirewall = mkOption {
      type = types.bool;
      default = false;
      description = ''
        Intentionally defaults to false. wad connects OUT to
        web.whatsapp.com over TLS; it does not listen on any TCP
        port. The unix socket lives on the local filesystem and
        never touches the network. Set this to true only if a
        future feature adds a REST primary adapter (not in the
        current roadmap).
      '';
    };

    extraArgs = mkOption {
      type = types.listOf types.str;
      default = [];
      example = ["--log-level=debug"];
      description = "Additional flags passed to wad. Appended after --profile.";
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.user != "root";
        message = "services.wa.user must not be root — wad refuses to run as root.";
      }
      {
        assertion = builtins.match "^[a-z][a-z0-9-]{0,30}[a-z0-9]$" cfg.profile != null;
        message = "services.wa.profile ${cfg.profile} does not match ^[a-z][a-z0-9-]{0,30}[a-z0-9]$ (see FR-002).";
      }
    ];

    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      home = cfg.dataDir;
      createHome = true;
      description = "wa daemon user";
    };

    users.groups.${cfg.group} = {};

    environment.systemPackages = [cfg.package];

    systemd.services."wad@${cfg.profile}" = {
      description = "wa daemon (${cfg.profile} profile)";
      documentation = ["https://github.com/yolo-labz/wa"];
      wantedBy = ["multi-user.target"];
      after = ["network-online.target"];
      wants = ["network-online.target"];

      environment = {
        XDG_DATA_HOME = cfg.dataDir;
        XDG_CONFIG_HOME = cfg.configDir;
        XDG_STATE_HOME = cfg.stateDir;
        XDG_CACHE_HOME = "${cfg.dataDir}/cache";
        XDG_RUNTIME_DIR = "/run/wa";
        WA_LOG_LEVEL = cfg.logLevel;
        GOTRACEBACK = "crash";
      };

      serviceConfig = {
        Type = "simple";
        User = cfg.user;
        Group = cfg.group;
        ExecStart = "${cfg.package}/bin/wad --profile ${cfg.profile} ${lib.escapeShellArgs cfg.extraArgs}";
        Restart = "on-failure";
        RestartSec = "5s";

        # Per-service state directory at /run/wa (tmpfs via
        # RuntimeDirectory) plus persistent /var/lib/wa.
        StateDirectory = "wa";
        StateDirectoryMode = "0700";
        RuntimeDirectory = "wa";
        RuntimeDirectoryMode = "0700";
        CacheDirectory = "wa";
        CacheDirectoryMode = "0700";
        LogsDirectory = "wa";
        LogsDirectoryMode = "0700";

        # Hardening — full set available in SYSTEM mode (unlike the
        # per-user template unit in contracts/service-templates.md,
        # which is limited to the subset that works in user mode).
        NoNewPrivileges = true;
        LockPersonality = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        SystemCallFilter = ["@system-service"];
        SystemCallArchitectures = "native";
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        RestrictNamespaces = true;
        RestrictAddressFamilies = ["AF_UNIX" "AF_INET" "AF_INET6"];
        CapabilityBoundingSet = [];
        AmbientCapabilities = [];

        # Go runtime is incompatible with MemoryDenyWriteExecute
        # (systemd#3814). Do NOT enable it here.
        MemoryDenyWriteExecute = false;

        # Writable paths needed by wad.
        ReadWritePaths = [
          cfg.dataDir
          cfg.configDir
          cfg.stateDir
        ];
      };
    };
  };
}
