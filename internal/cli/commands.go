package cli

import (
	"bufio"
	"context"
	"encoding/json"
	stderrors "errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ccgateway/internal/backend"
	builtinbackend "ccgateway/internal/backend/builtin"
	"ccgateway/internal/backends"
	"ccgateway/internal/config"
	"ccgateway/internal/control"
	"ccgateway/internal/doctor"
	cberr "ccgateway/internal/errors"
	installtx "ccgateway/internal/install"
	"ccgateway/internal/launchd"
	"ccgateway/internal/logx"
	modelnorm "ccgateway/internal/model"
	policyguard "ccgateway/internal/policy"
	"ccgateway/internal/provider"
	"ccgateway/internal/providers"
	"ccgateway/internal/scope"
	"ccgateway/internal/settingsguard"
	"ccgateway/internal/state"
)

type application struct {
	home            string
	cwd             string
	username        string
	registry        *provider.Registry
	backendRegistry *backend.Registry
}

type runtime struct {
	Ref     scope.Ref
	Paths   scope.Paths
	Config  config.Config
	State   state.State
	Bundle  provider.Bundle
	Backend backend.Bundle
	Logger  *logx.Logger
}

func Run(args []string) int {
	app, err := newApplication()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ccb] init failed: %v\n", err)
		return 1
	}
	if err := app.run(args); err != nil {
		app.logFailure(args, err)
		code := cberr.Code(err)
		if code == "" {
			code = cberr.ErrUnknown
		}
		fmt.Fprintf(os.Stderr, "[ccb] %s: %v\n", code, err)
		return 1
	}
	return 0
}

func newApplication() (*application, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	cwd := strings.TrimSpace(os.Getenv("CCB_CWD"))
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	return &application{
		home:            home,
		cwd:             cwd,
		username:        currentUsername(),
		registry:        providers.DefaultRegistry(),
		backendRegistry: backends.DefaultRegistry(),
	}, nil
}

func (a *application) run(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, usage())
	}
	switch args[0] {
	case "bootstrap":
		return a.cmdBootstrap(args[1:])
	case "setup":
		return a.cmdSetup(args[1:])
	case "proxy":
		return a.cmdProxy(args[1:])
	case "auth":
		return a.cmdAuth(args[1:])
	case "service":
		return a.cmdService(args[1:])
	case "gateway":
		return a.cmdGateway(args[1:])
	case "claude":
		return a.cmdClaude(args[1:])
	case "doctor":
		return a.cmdDoctor(args[1:])
	case "status", "ccb-status":
		return a.cmdStatus(args[1:])
	case "model":
		return a.cmdModel(args[1:])
	case "scope":
		return a.cmdScope(args[1:])
	case "preflight":
		return a.cmdPreflight(args[1:])
	case "handoff":
		return a.cmdHandoff(args[1:])
	case "failover":
		return a.cmdFailover(args[1:])
	case "use":
		return a.cmdUse(args[1:])
	case "uninstall":
		return a.cmdUninstall(args[1:])
	case "help", "-h", "--help":
		fmt.Println(usage())
		return nil
	default:
		return cberr.New(cberr.ErrInvalidArgs, usage())
	}
}

func (a *application) cmdGateway(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, gatewayUsage())
	}
	sub := args[0]
	if isHelpArg(sub) {
		fmt.Println(gatewayUsage())
		return nil
	}
	if sub != "serve" {
		return cberr.New(cberr.ErrInvalidArgs, gatewayUsage())
	}
	fs := flag.NewFlagSet("gateway serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "gateway config file")
	if err := fs.Parse(args[1:]); err != nil {
		return parseFlagError(err, gatewayUsage(), "failed to parse gateway flags")
	}
	if err := ensureNoExtraArgs(fs, gatewayUsage()); err != nil {
		return err
	}
	if strings.TrimSpace(*configPath) == "" {
		return cberr.New(cberr.ErrInvalidArgs, "--config is required")
	}
	cfg, err := builtinbackend.LoadConfig(*configPath)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load gateway config", err)
	}
	if cfg.BackendID != "builtin" {
		return cberr.New(cberr.ErrInvalidConfig, fmt.Sprintf("gateway backend mismatch in config: backend_id=%q (expected builtin)", cfg.BackendID))
	}
	if err := builtinbackend.Serve(*configPath); err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "builtin gateway serve failed", err)
	}
	return nil
}

func (a *application) cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	interactive := fs.Bool("interactive", false, "interactive setup prompts")
	runtimeMode := fs.String("runtime-mode", "", "gateway|native-cleanup|native-direct")
	gatewayBackend := fs.String("gateway-backend", "", "gateway backend id")
	settingsLayer := fs.String("settings-layer", "", "user|project|local")
	settingsPath := fs.String("settings-path", "", "settings file path")
	policyMode := fs.String("policy-mode", "", "strict|compat")
	model := fs.String("model", "", "model name")
	proxyVersion := fs.String("proxy-version", "", "tag|latest")
	skipClaudeApply := fs.Bool("skip-claude-apply", false, "skip claude apply")
	skipDoctor := fs.Bool("skip-doctor", false, "skip doctor")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, setupUsage(), "failed to parse setup flags")
	}
	if err := ensureNoExtraArgs(fs, setupUsage()); err != nil {
		return err
	}
	vendorValue := strings.TrimSpace(*vendorID)
	profileValue := strings.TrimSpace(*profileID)
	runtimeModeValue := strings.TrimSpace(*runtimeMode)
	gatewayBackendValue := strings.TrimSpace(*gatewayBackend)
	settingsLayerValue := strings.TrimSpace(*settingsLayer)
	settingsPathValue := strings.TrimSpace(*settingsPath)
	policyModeValue := strings.TrimSpace(*policyMode)
	modelValue := strings.TrimSpace(*model)

	if *interactive {
		prompted, err := a.collectSetupInteractiveInputs(setupInteractiveInput{
			VendorID:       vendorValue,
			ProfileID:      profileValue,
			RuntimeMode:    runtimeModeValue,
			GatewayBackend: gatewayBackendValue,
			SettingsLayer:  settingsLayerValue,
			Model:          modelValue,
		})
		if err != nil {
			return cberr.Wrap(cberr.ErrInvalidArgs, "interactive setup failed", err)
		}
		vendorValue = prompted.VendorID
		profileValue = prompted.ProfileID
		runtimeModeValue = prompted.RuntimeMode
		gatewayBackendValue = prompted.GatewayBackend
		settingsLayerValue = prompted.SettingsLayer
		modelValue = prompted.Model
	}

	ref, err := parseRequiredScope(vendorValue, profileValue)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "setup requires --vendor and --profile", err)
	}
	mode := config.DefaultForScope(a.home, a.cwd, ref.VendorID, ref.ProfileID).RuntimeMode
	if runtimeModeValue != "" {
		parsedMode, ok := config.ParseRuntimeMode(runtimeModeValue)
		if !ok {
			return cberr.New(cberr.ErrInvalidArgs, "--runtime-mode must be gateway|native-cleanup|native-direct")
		}
		mode = parsedMode
	}
	if settingsLayerValue != "" {
		switch config.SettingsLayer(settingsLayerValue) {
		case config.SettingsLayerUser, config.SettingsLayerProject, config.SettingsLayerLocal:
		default:
			return cberr.New(cberr.ErrInvalidArgs, "--settings-layer must be user|project|local")
		}
	}
	if policyModeValue != "" {
		normalizedPolicyMode, ok := policyguard.NormalizeMode(policyModeValue)
		if !ok {
			return cberr.New(cberr.ErrInvalidArgs, "--policy-mode must be strict|compat")
		}
		if strings.EqualFold(ref.VendorID, "codex") && normalizedPolicyMode != policyguard.ModeStrict {
			return cberr.New(cberr.ErrInvalidArgs, "--policy-mode compat is not allowed for vendor=codex")
		}
	}

	wrapStepErr := func(step string, inErr error) error {
		if inErr == nil {
			return nil
		}
		code := cberr.Code(inErr)
		if code == "" {
			code = cberr.ErrUnknown
		}
		return cberr.Wrap(code, "setup failed at "+step, inErr)
	}

	bootstrapArgs := []string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--runtime-mode", string(mode),
	}
	if v := gatewayBackendValue; v != "" {
		bootstrapArgs = append(bootstrapArgs, "--gateway-backend", v)
	}
	if v := settingsLayerValue; v != "" {
		bootstrapArgs = append(bootstrapArgs, "--settings-layer", v)
	}
	if v := settingsPathValue; v != "" {
		bootstrapArgs = append(bootstrapArgs, "--settings-path", v)
	}
	if v := policyModeValue; v != "" {
		bootstrapArgs = append(bootstrapArgs, "--policy-mode", v)
	}
	if v := modelValue; v != "" {
		bootstrapArgs = append(bootstrapArgs, "--model", v)
	}
	if err := a.cmdBootstrap(bootstrapArgs); err != nil {
		return wrapStepErr("bootstrap", err)
	}
	if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
		if mode == config.RuntimeModeGateway {
			proxyArgs := []string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}
			if v := strings.TrimSpace(*proxyVersion); v != "" {
				proxyArgs = append(proxyArgs, "--version", v)
			}
			if err := a.cmdProxy(proxyArgs); err != nil {
				return wrapStepErr("proxy install", err)
			}
			if err := a.cmdAuth([]string{"sync", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
				return wrapStepErr("auth sync", err)
			}
			if err := a.cmdService([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
				return wrapStepErr("service install", err)
			}
			if err := a.cmdService([]string{"start", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
				if recovered, recErr := a.recoverSetupServiceStart(ref, err); recErr != nil {
					return wrapStepErr("service start", recErr)
				} else if recovered {
					// recovered by service reinstall/start retry
				}
			}
		} else {
			if err := a.cleanupGatewayRuntime(ref); err != nil {
				return wrapStepErr("service cleanup", err)
			}
		}

		if !*skipClaudeApply {
			if err := a.cmdClaude([]string{"apply", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
				return wrapStepErr("claude apply", err)
			}
		}
		if !*skipDoctor {
			if err := a.cmdDoctor([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
				return wrapStepErr("doctor", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	fmt.Printf("setup complete (%s, mode=%s)\n", ref.ScopeID(), string(mode))
	return nil
}

func (a *application) recoverSetupServiceStart(ref scope.Ref, startErr error) (bool, error) {
	if !isRecoverableSetupServiceStartError(startErr) {
		return false, startErr
	}
	fmt.Printf("setup recovery: running service reconcile (%s)\n", ref.ScopeID())
	if err := a.runServiceReconcile(ref, true); err != nil {
		return true, cberr.Wrap(cberr.ErrRollbackFailed, "setup recovery failed during service reconcile", fmt.Errorf("initial service start error: %v; recovery reconcile error: %w", startErr, err))
	}
	fmt.Printf("setup recovery complete (%s)\n", ref.ScopeID())
	return true, nil
}

func (a *application) cleanupGatewayRuntime(ref scope.Ref) error {
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	mgr := launchd.NewManager()
	proxyLabel, syncLabel := serviceLabelsForRuntime(rt, a.username)
	proxyPlistPath, syncPlistPath := launchAgentPlistPaths(rt.Paths, proxyLabel, syncLabel)
	if err := mgr.RemoveAgents(proxyLabel, syncLabel, proxyPlistPath, syncPlistPath); err != nil {
		return cberr.Wrap(cberr.ErrLaunchctlFailed, "failed to cleanup existing launch agents", err)
	}
	rt.State.Service.Running = false
	if err := state.Save(rt.Paths.StatePath, rt.State); err != nil {
		return cberr.Wrap(cberr.ErrStateWriteFailed, "failed to persist state during service cleanup", err)
	}
	return nil
}

func (a *application) cmdBootstrap(args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	runtimeMode := fs.String("runtime-mode", "", "gateway|native-cleanup|native-direct")
	settingsLayer := fs.String("settings-layer", "", "user|project|local")
	settingsPath := fs.String("settings-path", "", "settings file path")
	policyMode := fs.String("policy-mode", "", "strict|compat")
	model := fs.String("model", "", "model name")
	port := fs.Int("port", -1, "port")
	authSource := fs.String("auth-source", "", "auth source file")
	authTarget := fs.String("auth-target", "", "auth target file")
	proxyEnabled := fs.String("proxy-enabled", "", "true|false")
	gatewayBackend := fs.String("gateway-backend", "", "gateway backend id")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, bootstrapUsage(), "failed to parse flags")
	}
	if err := ensureNoExtraArgs(fs, bootstrapUsage()); err != nil {
		return err
	}

	ref, err := parseRequiredScope(*vendorID, *profileID)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "bootstrap requires --vendor and --profile", err)
	}
	paths := scope.BuildPaths(a.home, a.cwd, ref)
	scopeExists := false
	if _, statErr := os.Stat(paths.ConfigPath); statErr == nil {
		scopeExists = true
	} else if !os.IsNotExist(statErr) {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to inspect scope config", statErr)
	}

	rt, err := a.loadRuntime(ref, true)
	if err != nil {
		return err
	}
	previousMode := rt.Config.RuntimeMode

	if *runtimeMode != "" {
		mode, ok := config.ParseRuntimeMode(*runtimeMode)
		if !ok {
			return cberr.New(cberr.ErrInvalidArgs, "--runtime-mode must be gateway|native-cleanup|native-direct")
		}
		rt.Config.RuntimeMode = mode
	}
	settingsLayerSpecified := false
	if *settingsLayer != "" {
		switch config.SettingsLayer(*settingsLayer) {
		case config.SettingsLayerUser, config.SettingsLayerProject, config.SettingsLayerLocal:
			rt.Config.SettingsLayer = config.SettingsLayer(*settingsLayer)
			settingsLayerSpecified = true
		default:
			return cberr.New(cberr.ErrInvalidArgs, "--settings-layer must be user|project|local")
		}
	}
	if *settingsPath != "" {
		rt.Config.SettingsPath = config.ExpandHome(*settingsPath, a.home)
	} else if settingsLayerSpecified {
		rt.Config.SettingsPath = ""
		rt.Config.SettingsPath = settingsguard.ResolveSettingsPath(rt.Paths, rt.Config)
	}
	if strings.TrimSpace(*policyMode) != "" {
		normalizedPolicyMode, ok := policyguard.NormalizeMode(*policyMode)
		if !ok {
			return cberr.New(cberr.ErrInvalidArgs, "--policy-mode must be strict|compat")
		}
		if strings.EqualFold(ref.VendorID, "codex") && normalizedPolicyMode != policyguard.ModeStrict {
			return cberr.New(cberr.ErrInvalidArgs, "--policy-mode compat is not allowed for vendor=codex")
		}
		rt.Config.PolicyMode = normalizedPolicyMode
	}
	if *model != "" {
		rt.Config.Model = *model
	}
	if *port > 0 {
		rt.Config.Port = *port
	}
	if *authSource != "" {
		rt.Config.AuthSource = config.ExpandHome(*authSource, a.home)
	}
	if *authTarget != "" {
		rt.Config.AuthTarget = config.ExpandHome(*authTarget, a.home)
	}
	if *proxyEnabled != "" {
		v, parseErr := parseBool(*proxyEnabled)
		if parseErr != nil {
			return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --proxy-enabled", parseErr)
		}
		rt.Config.ProxyEnabled = v
	} else {
		rt.Config.ProxyEnabled = rt.Config.RuntimeMode == config.RuntimeModeGateway
	}
	if *gatewayBackend != "" {
		rt.Config.GatewayBackend = strings.TrimSpace(*gatewayBackend)
	}
	normalizedModel, err := modelnorm.NormalizeForVendor(ref.VendorID, rt.Config.Model)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "invalid model for scope", err)
	}
	rt.Config.Model = normalizedModel
	if strings.TrimSpace(rt.Config.GatewayBackend) == "" {
		rt.Config.GatewayBackend = config.DefaultGatewayBackend
	}
	if rt.Config.RuntimeMode == config.RuntimeModeGateway && !rt.Config.ProxyEnabled {
		return cberr.New(cberr.ErrInvalidArgs, "gateway mode requires --proxy-enabled=true")
	}
	if rt.Config.RuntimeMode == config.RuntimeModeNativeCleanup && rt.Config.ProxyEnabled {
		return cberr.New(cberr.ErrInvalidArgs, "native-cleanup mode requires --proxy-enabled=false")
	}
	if rt.Config.RuntimeMode == config.RuntimeModeNativeDirect && rt.Config.ProxyEnabled {
		return cberr.New(cberr.ErrInvalidArgs, "native-direct mode requires --proxy-enabled=false")
	}
	if rt.Config.RuntimeMode == config.RuntimeModeNativeCleanup && !scopeExists {
		return cberr.New(cberr.ErrInvalidArgs, "runtime_mode=native-cleanup is cleanup-only; bootstrap gateway scope first")
	}
	if rt.Config.RuntimeMode == config.RuntimeModeNativeCleanup &&
		previousMode != config.RuntimeModeGateway &&
		previousMode != config.RuntimeModeNativeCleanup {
		return cberr.New(cberr.ErrInvalidArgs, "runtime_mode=native-cleanup is only allowed for cleanup from gateway scopes")
	}
	if rt.Config.SettingsPath == "" {
		rt.Config.SettingsPath = settingsguard.ResolveSettingsPath(rt.Paths, rt.Config)
	}
	if strings.TrimSpace(rt.Config.PolicyMode) == "" {
		rt.Config.PolicyMode = policyguard.DefaultModeForVendor(ref.VendorID)
	}
	if strings.EqualFold(ref.VendorID, "codex") {
		rt.Config.PolicyMode = policyguard.ModeStrict
	}
	if a.backendRegistry == nil {
		a.backendRegistry = backends.DefaultRegistry()
	}
	backendBundle, ok := a.backendRegistry.Get(rt.Config.GatewayBackend)
	if !ok && rt.Config.RuntimeMode == config.RuntimeModeGateway && rt.Config.ProxyEnabled {
		return cberr.New(cberr.ErrBackendMissing, fmt.Sprintf("backend %s not registered for scope", rt.Config.GatewayBackend))
	}
	if ok {
		rt.Backend = backendBundle
	} else {
		rt.Backend = backend.Bundle{ID: rt.Config.GatewayBackend}
	}

	if err := a.ensureModeSupported(rt); err != nil {
		return err
	}
	if err := a.enforcePolicy(rt, "bootstrap"); err != nil {
		return err
	}

	proxyLabel, syncLabel := serviceLabelsForRuntime(rt, a.username)
	if isGatewayProxyMode(rt) {
		portHealthProbe := func(port int) bool {
			if !backendCapabilityImplemented(rt.Backend, backend.CapabilityHealth) {
				return false
			}
			status, statusErr := launchd.NewManager().Status(proxyLabel, syncLabel)
			if statusErr != nil || !status.ProxyLoaded {
				return false
			}
			probe := rt
			probe.Config.Port = port
			return rt.Backend.Health.Check(context.Background(), toBackendRuntime(probe)) == nil
		}
		if err := control.WithSwitchLock(rt.Paths.SwitchLock, func() error {
			portValue, allocErr := control.AllocateScopePort(rt.Paths.PortsPath, ref.ScopeID(), rt.Config.Port, portHealthProbe)
			if allocErr != nil {
				return allocErr
			}
			rt.Config.Port = portValue
			return nil
		}); err != nil {
			return cberr.Wrap(cberr.ErrSwitchLockFailed, "failed to allocate scope port", err)
		}
	}

	rt.State.Scope = state.Scope{VendorID: ref.VendorID, ProfileID: ref.ProfileID}
	rt.State.RuntimeMode = string(rt.Config.RuntimeMode)
	if strings.TrimSpace(rt.State.Service.ProxyLabel) == "" {
		rt.State.Service.ProxyLabel = proxyLabel
	}
	if strings.TrimSpace(rt.State.Service.SyncLabel) == "" {
		rt.State.Service.SyncLabel = syncLabel
	}
	rt.State.Proxy.BinaryPath = rt.Paths.ProxyBinary
	rt.State.Backend.ID = rt.Config.GatewayBackend
	rt.State.Backend.BinaryPath = rt.Paths.ProxyBinary

	if err := config.Save(rt.Paths.ConfigPath, rt.Config); err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to save config", err)
	}
	if err := state.Save(rt.Paths.StatePath, rt.State); err != nil {
		return cberr.Wrap(cberr.ErrStateWriteFailed, "failed to save state", err)
	}

	if err := control.WithSwitchLock(rt.Paths.SwitchLock, func() error {
		active, err := control.LoadActive(rt.Paths.ActivePath)
		if err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
		}
		if active.ActiveVendor != "" {
			return nil
		}
		active.ActiveVendor = ref.VendorID
		active.ActiveProfile = ref.ProfileID
		active.ActiveGeneration = ""
		if err := control.SaveActive(rt.Paths.ActivePath, active); err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to initialize active pointer", err)
		}
		return nil
	}); err != nil {
		if cberr.Code(err) != cberr.ErrUnknown {
			return err
		}
		return cberr.Wrap(cberr.ErrSwitchLockFailed, "failed to coordinate active pointer initialization", err)
	}

	rt.Logger.Infof("bootstrap complete scope=%s", ref.ScopeID())
	if !suppressBootstrapOutput() {
		fmt.Printf("bootstrap complete (%s)\n", ref.ScopeID())
	}
	return nil
}

func (a *application) cmdProxy(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, proxyInstallUsage())
	}
	if isHelpArg(args[0]) {
		fmt.Println(proxyInstallUsage())
		return nil
	}
	if args[0] != "install" {
		return cberr.New(cberr.ErrInvalidArgs, proxyInstallUsage())
	}
	fs := flag.NewFlagSet("proxy install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	version := fs.String("version", "", "tag|latest")
	if err := fs.Parse(args[1:]); err != nil {
		return parseFlagError(err, proxyInstallUsage(), "failed to parse proxy flags")
	}
	if err := ensureNoExtraArgs(fs, proxyInstallUsage()); err != nil {
		return err
	}
	ref, err := parseRequiredScope(*vendorID, *profileID)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "proxy install requires --vendor and --profile", err)
	}
	if err := a.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		return err
	}
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	if err := a.enforcePolicy(rt, "proxy install"); err != nil {
		return err
	}
	if err := requireGatewayProxyMode(rt, "proxy install"); err != nil {
		return err
	}
	if err := a.ensureBackendCapability(rt, backend.CapabilityArtifact); err != nil {
		return err
	}
	installVersion := *version
	if installVersion == "" {
		installVersion = rt.Config.ProxyVersion
	}

	prevCfg := rt.Config
	prevState := rt.State
	var backupPath string
	var installResult backend.ArtifactResult
	tx := installtx.New(func(step string) error {
		return state.UpdateStep(rt.Paths.StatePath, &rt.State, step)
	})
	tx.Add("proxy.install.binary", func() error {
		if _, statErr := os.Stat(rt.Paths.ProxyBinary); statErr == nil {
			backupPath = rt.Paths.ProxyBinary + ".bak"
			if err := copyFile(rt.Paths.ProxyBinary, backupPath, 0o755); err != nil {
				return cberr.Wrap(cberr.ErrDownloadFailed, "failed to backup existing binary", err)
			}
		}
		res, err := rt.Backend.Artifact.Install(context.Background(), toBackendRuntime(rt), installVersion)
		if err != nil {
			return err
		}
		installResult = res
		return nil
	}, func() error {
		if backupPath != "" {
			return copyFile(backupPath, rt.Paths.ProxyBinary, 0o755)
		}
		_ = os.Remove(rt.Paths.ProxyBinary)
		return nil
	})
	tx.Add("proxy.update.config", func() error {
		rt.Config.ProxyVersion = installResult.Version
		return config.Save(rt.Paths.ConfigPath, rt.Config)
	}, func() error {
		return config.Save(rt.Paths.ConfigPath, prevCfg)
	})
	tx.Add("proxy.update.state", func() error {
		rt.State.Proxy.Version = installResult.Version
		rt.State.Proxy.BinaryPath = installResult.BinaryPath
		rt.State.Proxy.SHA256 = installResult.SHA256
		rt.State.Backend.ID = rt.Config.GatewayBackend
		rt.State.Backend.Version = installResult.Version
		rt.State.Backend.BinaryPath = installResult.BinaryPath
		rt.State.Backend.SHA256 = installResult.SHA256
		return state.Save(rt.Paths.StatePath, rt.State)
	}, func() error {
		return state.Save(rt.Paths.StatePath, prevState)
	})
	if err := tx.Run(); err != nil {
		var rbErr *installtx.RollbackError
		if stderrors.As(err, &rbErr) {
			return cberr.Wrap(cberr.ErrRollbackFailed, "proxy install failed and rollback was required", err)
		}
		return err
	}
	if backupPath != "" {
		_ = os.Remove(backupPath)
	}
	rt.Logger.Infof("proxy install complete scope=%s version=%s", ref.ScopeID(), installResult.Version)
	fmt.Printf("proxy install complete (%s, %s)\n", ref.ScopeID(), installResult.Version)
	return nil
}

func (a *application) cmdAuth(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, authSyncUsage())
	}
	if isHelpArg(args[0]) {
		fmt.Println(authSyncUsage())
		return nil
	}
	if args[0] != "sync" {
		return cberr.New(cberr.ErrInvalidArgs, authSyncUsage())
	}
	fs := flag.NewFlagSet("auth sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	if err := fs.Parse(args[1:]); err != nil {
		return parseFlagError(err, authSyncUsage(), "failed to parse auth flags")
	}
	if err := ensureNoExtraArgs(fs, authSyncUsage()); err != nil {
		return err
	}
	ref, err := parseRequiredScope(*vendorID, *profileID)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "auth sync requires --vendor and --profile", err)
	}
	if err := a.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		return err
	}
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	if err := a.enforcePolicy(rt, "auth sync"); err != nil {
		return err
	}
	if err := a.ensureProviderCapability(rt, provider.CapabilityAuth); err != nil {
		return err
	}
	if err := rt.Bundle.Auth.Sync(context.Background(), toProviderRuntime(rt)); err != nil {
		return err
	}
	rt.Logger.Infof("auth sync complete scope=%s", ref.ScopeID())
	fmt.Printf("auth sync complete (%s)\n", ref.ScopeID())
	return nil
}

func (a *application) cmdService(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, serviceUsage())
	}
	sub := args[0]
	if isHelpArg(sub) {
		fmt.Println(serviceUsage())
		return nil
	}
	if !isServiceSubcommand(sub) {
		return cberr.New(cberr.ErrInvalidArgs, serviceUsage())
	}
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	active := fs.Bool("active", false, "use active scope")
	if err := fs.Parse(args[1:]); err != nil {
		return parseFlagError(err, serviceUsage(), "failed to parse service flags")
	}
	if err := ensureNoExtraArgs(fs, serviceUsage()); err != nil {
		return err
	}

	readOnly := sub == "status"
	if !readOnly && *active {
		return cberr.New(cberr.ErrInvalidArgs, "--active can only be used with 'ccb service status'")
	}
	ref, err := a.resolveScope(*vendorID, *profileID, *active, readOnly)
	if err != nil {
		return err
	}
	if !readOnly {
		if err := a.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
			return err
		}
	}
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	if !readOnly {
		if err := a.enforcePolicy(rt, "service "+sub); err != nil {
			return err
		}
	}
	mgr := launchd.NewManager()
	proxyLabel, syncLabel := serviceLabelsForRuntime(rt, a.username)
	proxyPlistPath, syncPlistPath := launchAgentPlistPaths(rt.Paths, proxyLabel, syncLabel)
	if err := requireGatewayProxyMode(rt, "service command"); err != nil {
		return err
	}

	switch sub {
	case "install":
		if err := a.ensureBackendCapability(rt, backend.CapabilityProxy); err != nil {
			return err
		}
		if _, statErr := os.Stat(rt.Paths.ProxyBinary); statErr != nil {
			return cberr.New(cberr.ErrInvalidConfig, "proxy binary missing; run 'ccb proxy install --vendor ... --profile ...'")
		}
		execPath, err := os.Executable()
		if err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to resolve executable path", err)
		}
		files := launchd.AgentFiles{
			ProxyPlistPath: proxyPlistPath,
			SyncPlistPath:  syncPlistPath,
			ProxyBinary:    rt.Paths.ProxyBinary,
			ProxyConfig:    rt.Paths.ProxyConfig,
			ProxyLog:       rt.Paths.ProxyLogPath,
			SyncLog:        rt.Paths.SyncLogPath,
			SyncScript:     rt.Paths.SyncScriptPath,
			AuthSource:     rt.Config.AuthSource,
			HomeDir:        a.home,
			ProxyLabel:     proxyLabel,
			SyncLabel:      syncLabel,
		}
		prevState := rt.State
		tx := installtx.New(func(step string) error {
			return state.UpdateStep(rt.Paths.StatePath, &rt.State, step)
		})
		tx.Add("service.write.proxy.config", func() error {
			return rt.Backend.Proxy.WriteProxyConfig(context.Background(), toBackendRuntime(rt))
		}, func() error {
			_ = os.Remove(rt.Paths.ProxyConfig)
			return nil
		})
		tx.Add("service.write.sync.script", func() error {
			return rt.Backend.Proxy.WriteSyncScript(context.Background(), toBackendRuntime(rt), execPath)
		}, func() error {
			_ = os.Remove(rt.Paths.SyncScriptPath)
			return nil
		})
		tx.Add("service.install.launchd", func() error {
			if err := mgr.InstallAgents(files); err != nil {
				return cberr.Wrap(cberr.ErrLaunchctlFailed, "failed to install launch agents", err)
			}
			return nil
		}, func() error {
			return mgr.RemoveAgents(proxyLabel, syncLabel, proxyPlistPath, syncPlistPath)
		})
		tx.Add("service.update.state", func() error {
			rt.State.Service.ProxyLabel = proxyLabel
			rt.State.Service.SyncLabel = syncLabel
			rt.State.Service.Running = false
			return state.Save(rt.Paths.StatePath, rt.State)
		}, func() error {
			return state.Save(rt.Paths.StatePath, prevState)
		})
		if err := tx.Run(); err != nil {
			var rbErr *installtx.RollbackError
			if stderrors.As(err, &rbErr) {
				return cberr.Wrap(cberr.ErrRollbackFailed, "service install failed and rollback was required", err)
			}
			return err
		}
		rt.Logger.Infof("service install complete scope=%s", ref.ScopeID())
		fmt.Printf("service install complete (%s)\n", ref.ScopeID())
		return nil
	case "start":
		if err := a.ensureBackendCapability(rt, backend.CapabilityHealth); err != nil {
			return err
		}
		if err := mgr.Start(proxyLabel, syncLabel); err != nil {
			if !disableStartRecovery() && isRecoverableSetupServiceStartError(err) {
				if recErr := a.runServiceReconcile(ref, false); recErr == nil {
					rt.Logger.Infof("service start auto-recovered via reconcile scope=%s", ref.ScopeID())
					fmt.Printf("service started (%s, reconciled)\n", ref.ScopeID())
					return nil
				}
			}
			return cberr.Wrap(cberr.ErrLaunchctlFailed, "failed to start service", err)
		}
		if err := waitForBackendHealth(rt, 8*time.Second, 250*time.Millisecond); err != nil {
			stopErr := mgr.Stop(proxyLabel, syncLabel)
			if stopErr != nil {
				rt.State.Service.Running = true
				if saveErr := state.Save(rt.Paths.StatePath, rt.State); saveErr != nil {
					return cberr.Wrap(cberr.ErrRollbackFailed, "service started but healthcheck failed, rollback stop failed, and state persistence failed", fmt.Errorf("healthcheck failed: %v; stop failed: %v; state save failed: %w", err, stopErr, saveErr))
				}
				return cberr.Wrap(cberr.ErrRollbackFailed, "service started but healthcheck failed and rollback stop failed", fmt.Errorf("healthcheck failed: %v; stop failed: %w", err, stopErr))
			}
			rt.State.Service.Running = false
			if saveErr := state.Save(rt.Paths.StatePath, rt.State); saveErr != nil {
				return cberr.Wrap(cberr.ErrStateWriteFailed, "service started but healthcheck failed and service stopped; failed to persist state", fmt.Errorf("healthcheck failed: %v; state save failed: %w", err, saveErr))
			}
			return cberr.Wrap(cberr.ErrSwitchValidation, "service started but healthcheck failed; service stopped", err)
		}
		rt.State.Service.Running = true
		if err := state.Save(rt.Paths.StatePath, rt.State); err != nil {
			return cberr.Wrap(cberr.ErrStateWriteFailed, "service started but failed to persist state", err)
		}
		rt.Logger.Infof("service start complete scope=%s", ref.ScopeID())
		fmt.Printf("service started (%s)\n", ref.ScopeID())
		return nil
	case "reconcile":
		if err := a.runServiceReconcile(ref, false); err != nil {
			return err
		}
		rt.Logger.Infof("service reconcile complete scope=%s", ref.ScopeID())
		fmt.Printf("service reconciled (%s)\n", ref.ScopeID())
		return nil
	case "stop":
		if err := mgr.Stop(proxyLabel, syncLabel); err != nil {
			return cberr.Wrap(cberr.ErrLaunchctlFailed, "failed to stop service", err)
		}
		rt.State.Service.Running = false
		if err := state.Save(rt.Paths.StatePath, rt.State); err != nil {
			return cberr.Wrap(cberr.ErrStateWriteFailed, "service stopped but failed to persist state", err)
		}
		rt.Logger.Infof("service stop complete scope=%s", ref.ScopeID())
		fmt.Printf("service stopped (%s)\n", ref.ScopeID())
		return nil
	case "status":
		if err := a.ensureBackendCapability(rt, backend.CapabilityHealth); err != nil {
			return err
		}
		status, err := mgr.Status(proxyLabel, syncLabel)
		if err != nil {
			return cberr.Wrap(cberr.ErrLaunchctlFailed, "failed to read launchd status", err)
		}
		healthErr := rt.Backend.Health.Check(context.Background(), toBackendRuntime(rt))
		fmt.Printf("scope: %s\n", ref.ScopeID())
		fmt.Printf("proxy loaded: %t\n", status.ProxyLoaded)
		fmt.Printf("sync loaded: %t\n", status.SyncLoaded)
		fmt.Printf("health: %t\n", healthErr == nil)
		if healthErr != nil {
			fmt.Printf("health detail: %v\n", healthErr)
		}
		return nil
	default:
		return cberr.New(cberr.ErrInvalidArgs, serviceUsage())
	}
}

func (a *application) cmdClaude(args []string) error {
	return a.cmdClaudeWithExpected(args, nil)
}

func (a *application) cmdClaudeWithExpected(args []string, expected *expectedActiveRequirement) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, claudeUsage())
	}
	sub := args[0]
	if isHelpArg(sub) {
		fmt.Println(claudeUsage())
		return nil
	}
	if sub != "apply" && sub != "revert" {
		return cberr.New(cberr.ErrInvalidArgs, claudeUsage())
	}
	fs := flag.NewFlagSet("claude", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	if err := fs.Parse(args[1:]); err != nil {
		return parseFlagError(err, claudeUsage(), "failed to parse claude flags")
	}
	if err := ensureNoExtraArgs(fs, claudeUsage()); err != nil {
		return err
	}
	ref, err := parseRequiredScope(*vendorID, *profileID)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "claude command requires --vendor and --profile", err)
	}
	if err := a.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		return err
	}
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	if err := a.ensureProviderCapability(rt, provider.CapabilityClaude); err != nil {
		return err
	}
	if sub == "apply" {
		if err := ensureScopedSettingsBinding(rt, "claude apply"); err != nil {
			return err
		}
		if err := a.enforcePolicy(rt, "claude apply"); err != nil {
			return err
		}
	}

	switch sub {
	case "apply":
		if err := settingsguard.CheckEffectiveOverride(rt.Paths, rt.Config); err != nil {
			return cberr.Wrap(cberr.ErrConfigOverridden, "effective settings would override target scope", err)
		}
		generation := control.NextGeneration()
		prevState := rt.State
		preservedSnapshotPath, preservedSnapshotSHA, preserveSnapshot := reusableClaudeSnapshot(rt.State)
		var prevActive control.ActivePointer
		activeTouched := false
		var snapshotPath string
		var snapshotSHA string

		tx := installtx.New(func(step string) error {
			return state.UpdateStep(rt.Paths.StatePath, &rt.State, step)
		})
		tx.Add("claude.apply", func() error {
			res, err := rt.Bundle.Claude.Apply(context.Background(), toProviderRuntime(rt), generation)
			if err != nil {
				return err
			}
			snapshotPath = res.SnapshotPath
			snapshotSHA = res.SnapshotSHA256
			return nil
		}, func() error {
			if snapshotPath == "" {
				return nil
			}
			return rt.Bundle.Claude.Revert(context.Background(), toProviderRuntime(rt), snapshotPath, snapshotSHA)
		})
		tx.Add("claude.update.state", func() error {
			rt.State.Claude.Applied = true
			if preserveSnapshot {
				rt.State.Claude.SnapshotPath = preservedSnapshotPath
				rt.State.Claude.SnapshotSHA256 = preservedSnapshotSHA
			} else {
				rt.State.Claude.SnapshotPath = snapshotPath
				rt.State.Claude.SnapshotSHA256 = snapshotSHA
			}
			rt.State.Claude.AppliedGeneration = generation
			return state.Save(rt.Paths.StatePath, rt.State)
		}, func() error {
			return state.Save(rt.Paths.StatePath, prevState)
		})
		tx.Add("claude.update.active", func() error {
			active, err := control.LoadActive(rt.Paths.ActivePath)
			if err != nil {
				return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
			}
			if active.ActiveVendor != ref.VendorID || active.ActiveProfile != ref.ProfileID {
				return nil
			}
			prevActive = active
			active.ActiveGeneration = generation
			if err := control.SaveActive(rt.Paths.ActivePath, active); err != nil {
				return cberr.Wrap(cberr.ErrSwitchFailed, "failed to update active generation", err)
			}
			activeTouched = true
			return nil
		}, func() error {
			if !activeTouched {
				return nil
			}
			if err := control.SaveActive(rt.Paths.ActivePath, prevActive); err != nil {
				return cberr.Wrap(cberr.ErrRollbackFailed, "failed to restore active pointer", err)
			}
			return nil
		})
		if err := control.WithSwitchLock(rt.Paths.SwitchLock, func() error {
			activeBefore, err := control.LoadActive(rt.Paths.ActivePath)
			if err != nil {
				return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
			}
			if expected != nil {
				if err := ensureExpectedActive(activeBefore, *expected, "claude apply"); err != nil {
					return err
				}
			}
			return tx.Run()
		}); err != nil {
			var rbErr *installtx.RollbackError
			if stderrors.As(err, &rbErr) {
				return cberr.Wrap(cberr.ErrRollbackFailed, "claude apply failed and rollback was required", err)
			}
			if cberr.Code(err) != cberr.ErrUnknown {
				return err
			}
			return cberr.Wrap(cberr.ErrSwitchLockFailed, "failed to coordinate claude apply", err)
		}
		rt.Logger.Infof("claude apply complete scope=%s generation=%s", ref.ScopeID(), generation)
		fmt.Printf("claude settings applied (%s)\n", ref.ScopeID())
		return nil
	case "revert":
		if !rt.State.Claude.Applied {
			fmt.Printf("claude settings are already clean (%s)\n", ref.ScopeID())
			return nil
		}
		if err := control.WithSwitchLock(rt.Paths.SwitchLock, func() error {
			active, err := control.LoadActive(rt.Paths.ActivePath)
			if err != nil {
				return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
			}
			if !allowSettingsMutation(active, ref, rt.State.Claude.AppliedGeneration) {
				return cberr.New(cberr.ErrGenerationMismatch, "unsafe revert blocked: scope/generation does not match active pointer")
			}
			if err := rt.Bundle.Claude.Revert(context.Background(), toProviderRuntime(rt), rt.State.Claude.SnapshotPath, rt.State.Claude.SnapshotSHA256); err != nil {
				return err
			}
			rt.State.Claude.Applied = false
			rt.State.Claude.SnapshotPath = ""
			rt.State.Claude.SnapshotSHA256 = ""
			rt.State.Claude.AppliedGeneration = ""
			if err := state.Save(rt.Paths.StatePath, rt.State); err != nil {
				return cberr.Wrap(cberr.ErrStateWriteFailed, "failed to persist state", err)
			}
			return nil
		}); err != nil {
			if cberr.Code(err) != cberr.ErrUnknown {
				return err
			}
			return cberr.Wrap(cberr.ErrSwitchLockFailed, "failed to coordinate claude revert", err)
		}
		rt.Logger.Infof("claude revert complete scope=%s", ref.ScopeID())
		fmt.Printf("claude settings reverted (%s)\n", ref.ScopeID())
		return nil
	default:
		return cberr.New(cberr.ErrInvalidArgs, claudeUsage())
	}
}

func (a *application) cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	activeFlag := fs.Bool("active", false, "use active scope")
	clearErrorHistory := fs.Bool("clear-error-history", false, "clear past error history for this scope")
	verbose := fs.Bool("verbose", false, "show full diagnostics/history")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, doctorUsage(), "failed to parse doctor flags")
	}
	if err := ensureNoExtraArgs(fs, doctorUsage()); err != nil {
		return err
	}
	ref, err := a.resolveScope(*vendorID, *profileID, *activeFlag, true)
	if err != nil {
		return err
	}
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	active, err := control.LoadActive(rt.Paths.ActivePath)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
	}
	if *clearErrorHistory {
		if err := doctor.ClearErrorHistory(rt.Paths.AppLogPath); err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to clear doctor error history", err)
		}
	}
	report := doctor.RunScoped(doctor.ScopedInput{
		Scope:  ref,
		Paths:  rt.Paths,
		Config: rt.Config,
		State:  rt.State,
		Active: active,
	})
	if !*verbose && len(report.RecentErrors) > 1 {
		report.RecentErrors = report.RecentErrors[len(report.RecentErrors)-1:]
	}
	fmt.Println(report.Render())
	if report.HasFailures() {
		return cberr.New(cberr.ErrDoctorFailed, "doctor found failing checks")
	}
	return nil
}

func (a *application) cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	activeFlag := fs.Bool("active", false, "use active scope")
	since := fs.String("since", "24h", "usage aggregation window (Go duration)")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, statusUsage(), "failed to parse status flags")
	}
	if err := ensureNoExtraArgs(fs, statusUsage()); err != nil {
		return err
	}
	sinceDur, err := time.ParseDuration(strings.TrimSpace(*since))
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --since duration", err)
	}
	if sinceDur <= 0 {
		return cberr.New(cberr.ErrInvalidArgs, "--since must be > 0")
	}

	ref, err := a.resolveScope(*vendorID, *profileID, *activeFlag, true)
	if err != nil {
		return err
	}
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}

	policyEval := policyguard.Evaluate(rt.Paths, rt.Config)
	policyOK := policyEval.Mode != policyguard.ModeStrict || len(policyEval.Violations) == 0

	mgr := launchd.NewManager()
	proxyLabel, syncLabel := serviceLabelsForRuntime(rt, a.username)
	proxyLoaded := false
	syncLoaded := false
	healthKnown := false
	healthOK := false
	healthDetail := ""
	if isGatewayProxyMode(rt) {
		status, statusErr := mgr.Status(proxyLabel, syncLabel)
		if statusErr != nil {
			healthDetail = fmt.Sprintf("launchd status error: %v", statusErr)
		} else {
			proxyLoaded = status.ProxyLoaded
			syncLoaded = status.SyncLoaded
		}
		if backendCapabilityImplemented(rt.Backend, backend.CapabilityHealth) {
			healthKnown = true
			healthErr := rt.Backend.Health.Check(context.Background(), toBackendRuntime(rt))
			if healthErr != nil {
				healthDetail = healthErr.Error()
			} else {
				healthOK = true
			}
		}
	}

	active, activeErr := control.LoadActive(rt.Paths.ActivePath)
	activeScope := "<unset>"
	activeGeneration := ""
	if activeErr == nil {
		if strings.TrimSpace(active.ActiveVendor) != "" && strings.TrimSpace(active.ActiveProfile) != "" {
			activeScope = fmt.Sprintf("%s:%s", active.ActiveVendor, active.ActiveProfile)
			activeGeneration = active.ActiveGeneration
		}
	}
	routeProof := doctor.CheckResult{Name: "route proof", OK: false, Detail: fmt.Sprintf("unknown (failed to load active pointer: %v)", activeErr)}
	settingsTarget := doctor.CheckResult{Name: "settings target", OK: false, Detail: "unavailable"}
	toolIntegrity := doctor.CheckResult{Name: "tool-call integrity", OK: false, Detail: "unavailable"}
	modelProof := doctor.CheckResult{Name: "model proof", OK: false, Detail: "unavailable"}
	if activeErr != nil {
		// keep defaults above
	} else {
		report := doctor.RunScoped(doctor.ScopedInput{
			Scope:  ref,
			Paths:  rt.Paths,
			Config: rt.Config,
			State:  rt.State,
			Active: active,
		})
		if check, ok := findCheck(report.Checks, "route proof"); ok {
			routeProof = check
		}
		if check, ok := findCheck(report.Checks, "settings target"); ok {
			settingsTarget = check
		}
		if check, ok := findCheck(report.Checks, "tool-call integrity"); ok {
			toolIntegrity = check
		}
		if check, ok := findCheck(report.Checks, "model proof"); ok {
			modelProof = check
		}
	}

	usageAvailable := false
	var usageSummary proxyLogSummary
	usageErrText := ""
	if isGatewayProxyMode(rt) {
		summary, sumErr := summarizeProxyLog(rt.Paths.ProxyLogPath, sinceDur, time.Now())
		if sumErr != nil {
			usageErrText = sumErr.Error()
		} else {
			usageAvailable = true
			usageSummary = summary
		}
	}

	if *jsonOutput {
		payload := map[string]any{
			"scope":                   ref.ScopeID(),
			"active_scope":            activeScope,
			"active_generation":       activeGeneration,
			"is_active_scope":         activeScope == ref.ScopeID(),
			"runtime_mode":            rt.Config.RuntimeMode,
			"backend":                 rt.Config.GatewayBackend,
			"model":                   rt.Config.Model,
			"port":                    rt.Config.Port,
			"effective_settings_path": rt.Config.SettingsPath,
			"policy_mode":             policyEval.Mode,
			"policy_guard_ok":         policyOK,
			"route_proof_ok":          routeProof.OK,
			"route_proof_detail":      routeProof.Detail,
			"model_proof_ok":          modelProof.OK,
			"model_proof_detail":      modelProof.Detail,
			"settings_target_ok":      settingsTarget.OK,
			"settings_target_detail":  settingsTarget.Detail,
			"tool_integrity_ok":       toolIntegrity.OK,
			"tool_integrity_detail":   toolIntegrity.Detail,
			"since":                   sinceDur.String(),
			"usage_available":         usageAvailable,
		}
		if !policyOK {
			payload["policy_violation"] = policyEval.Violations[0]
		}
		if isGatewayProxyMode(rt) {
			payload["proxy_loaded"] = proxyLoaded
			payload["sync_loaded"] = syncLoaded
			if healthKnown {
				payload["health_ok"] = healthOK
			} else {
				payload["health_ok"] = nil
			}
			payload["health_detail"] = healthDetail
		} else {
			payload["proxy_loaded"] = nil
			payload["sync_loaded"] = nil
			payload["health_ok"] = nil
		}
		if usageAvailable {
			payload["usage"] = map[string]any{
				"total":        usageSummary.Total,
				"chat":         usageSummary.Chat,
				"count_tokens": usageSummary.CountTokens,
				"top_paths":    usageSummary.TopPaths(5),
			}
		} else {
			payload["usage_error"] = usageErrText
		}
		encoded, encErr := json.MarshalIndent(payload, "", "  ")
		if encErr != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to encode status json", encErr)
		}
		fmt.Println(string(encoded))
		return nil
	}

	fmt.Printf("scope: %s\n", ref.ScopeID())
	fmt.Printf("active scope: %s\n", activeScope)
	fmt.Printf("runtime mode: %s\n", rt.Config.RuntimeMode)
	fmt.Printf("backend: %s\n", rt.Config.GatewayBackend)
	fmt.Printf("model: %s\n", rt.Config.Model)
	fmt.Printf("port: %d\n", rt.Config.Port)
	fmt.Printf("effective settings target: %s\n", rt.Config.SettingsPath)
	if routeProof.OK {
		fmt.Printf("route proof: OK - %s\n", routeProof.Detail)
	} else {
		fmt.Printf("route proof: FAIL - %s\n", routeProof.Detail)
	}
	if modelProof.OK {
		fmt.Printf("model proof: OK - %s\n", modelProof.Detail)
	} else {
		fmt.Printf("model proof: FAIL - %s\n", modelProof.Detail)
	}
	if settingsTarget.OK {
		fmt.Printf("settings target: OK - %s\n", settingsTarget.Detail)
	} else {
		fmt.Printf("settings target: FAIL - %s\n", settingsTarget.Detail)
	}
	if toolIntegrity.OK {
		fmt.Printf("tool-call integrity: OK - %s\n", toolIntegrity.Detail)
	} else {
		fmt.Printf("tool-call integrity: FAIL - %s\n", toolIntegrity.Detail)
	}
	fmt.Printf("policy mode: %s\n", policyEval.Mode)
	fmt.Printf("policy guard: %t\n", policyOK)
	if !policyOK {
		fmt.Printf("policy detail: %s\n", policyEval.Violations[0])
	}

	if isGatewayProxyMode(rt) {
		fmt.Printf("proxy loaded: %t\n", proxyLoaded)
		fmt.Printf("sync loaded: %t\n", syncLoaded)
		if healthKnown {
			fmt.Printf("health: %t\n", healthOK)
			if !healthOK && strings.TrimSpace(healthDetail) != "" {
				fmt.Printf("health detail: %s\n", healthDetail)
			}
		} else {
			fmt.Println("health: unknown (backend has no health checker)")
		}
	} else {
		fmt.Printf("proxy loaded: n/a (runtime_mode=%s)\n", rt.Config.RuntimeMode)
		fmt.Printf("sync loaded: n/a (runtime_mode=%s)\n", rt.Config.RuntimeMode)
		fmt.Printf("health: n/a (runtime_mode=%s)\n", rt.Config.RuntimeMode)
	}

	fmt.Printf("usage window: last %s\n", sinceDur)
	if usageAvailable {
		fmt.Printf("usage local: total=%d chat=%d count_tokens=%d\n", usageSummary.Total, usageSummary.Chat, usageSummary.CountTokens)
		top := usageSummary.TopPaths(5)
		if len(top) > 0 {
			fmt.Println("usage local top paths:")
			for _, item := range top {
				fmt.Printf("- %s: %d\n", item.Path, item.Count)
			}
		}
	} else if isGatewayProxyMode(rt) {
		fmt.Printf("usage local: unavailable (%s)\n", usageErrText)
	} else {
		fmt.Printf("usage local: n/a (runtime_mode=%s)\n", rt.Config.RuntimeMode)
	}

	if strings.EqualFold(ref.VendorID, "codex") {
		fmt.Println("vendor note: local usage above is proxy traffic summary, not Codex account billing usage.")
	}
	return nil
}

func (a *application) cmdModel(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, modelUsage())
	}
	sub := args[0]
	if isHelpArg(sub) {
		fmt.Println(modelUsage())
		return nil
	}
	if sub != "switch" {
		return cberr.New(cberr.ErrInvalidArgs, modelUsage())
	}

	fs := flag.NewFlagSet("model switch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	modelName := fs.String("model", "", "model name")
	if err := fs.Parse(args[1:]); err != nil {
		return parseFlagError(err, modelUsage(), "failed to parse model flags")
	}
	if err := ensureNoExtraArgs(fs, modelUsage()); err != nil {
		return err
	}
	ref, err := parseRequiredScope(*vendorID, *profileID)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "model switch requires --vendor and --profile", err)
	}
	normalizedModel, err := modelnorm.NormalizeForVendor(ref.VendorID, *modelName)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "model switch requires --model", err)
	}

	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	if err := a.enforcePolicy(rt, "model switch"); err != nil {
		return err
	}
	if err := ensureScopedSettingsBinding(rt, "model switch"); err != nil {
		return err
	}
	if err := requireGatewayProxyMode(rt, "model switch"); err != nil {
		return err
	}
	if err := a.ensureBackendCapability(rt, backend.CapabilityProxy); err != nil {
		return err
	}
	if err := a.ensureBackendCapability(rt, backend.CapabilityHealth); err != nil {
		return err
	}
	if err := a.ensureProviderCapability(rt, provider.CapabilityClaude); err != nil {
		return err
	}

	prevActive, err := control.LoadActive(rt.Paths.ActivePath)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
	}
	if prevActive.ActiveVendor != ref.VendorID || prevActive.ActiveProfile != ref.ProfileID {
		activeScope := "<unset>"
		if strings.TrimSpace(prevActive.ActiveVendor) != "" && strings.TrimSpace(prevActive.ActiveProfile) != "" {
			activeScope = fmt.Sprintf("%s:%s", prevActive.ActiveVendor, prevActive.ActiveProfile)
		}
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("model switch is active-scope only (active=%s target=%s); run: ccb use --vendor %s --profile %s", activeScope, ref.ScopeID(), ref.VendorID, ref.ProfileID),
		)
	}
	if err := ensureActiveSwitchContract(prevActive, rt.State, ref, "model switch"); err != nil {
		return err
	}
	if _, statErr := os.Stat(rt.Paths.ProxyBinary); statErr != nil {
		return cberr.New(
			cberr.ErrInvalidConfig,
			fmt.Sprintf(
				"proxy binary missing (%s); run: ccb setup --vendor %s --profile %s --gateway-backend %s --model %s (or: ccb proxy install --vendor %s --profile %s)",
				rt.Paths.ProxyBinary,
				ref.VendorID,
				ref.ProfileID,
				nonEmptyOrDefault(rt.Config.GatewayBackend, config.DefaultGatewayBackend),
				normalizedModel,
				ref.VendorID,
				ref.ProfileID,
			),
		)
	}

	settingsSnapshotPath, settingsSnapshotSHA, err := snapshotSettingsBackup(rt.Config.SettingsPath, rt.Paths.SnapshotsDir, "model-switch")
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to snapshot current Claude settings", err)
	}

	prevCfg := rt.Config
	prevState := rt.State
	tx := installtx.New(func(step string) error {
		current, loadErr := state.Load(rt.Paths.StatePath)
		if loadErr != nil {
			return loadErr
		}
		current.LastStep = step
		if saveErr := state.Save(rt.Paths.StatePath, current); saveErr != nil {
			return saveErr
		}
		rt.State = current
		return nil
	})
	tx.Add("model.switch.update.config", func() error {
		rt.Config.Model = normalizedModel
		return config.Save(rt.Paths.ConfigPath, rt.Config)
	}, func() error {
		var rollbackErrs []error
		rt.Config = prevCfg
		if err := config.Save(rt.Paths.ConfigPath, prevCfg); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore config: %w", err))
		}
		if prevCfg.RuntimeMode == config.RuntimeModeGateway && prevCfg.ProxyEnabled {
			installErr := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
				return a.cmdService([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID})
			})
			if installErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to reinstall previous model service config: %w", installErr))
			}
			if installErr == nil {
				if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
					return a.cmdService([]string{"start", "--vendor", ref.VendorID, "--profile", ref.ProfileID})
				}); err != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restart previous model service: %w", err))
				}
			}
		}
		activeNow, err := control.LoadActive(rt.Paths.ActivePath)
		if err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to inspect active pointer during rollback: %w", err))
		} else if activeNow.ActiveVendor == ref.VendorID && activeNow.ActiveProfile == ref.ProfileID {
			if err := control.SaveActive(rt.Paths.ActivePath, prevActive); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore active pointer: %w", err))
			}
		}
		if err := state.Save(rt.Paths.StatePath, prevState); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore state: %w", err))
		}
		if len(rollbackErrs) == 0 {
			return nil
		}
		return stderrors.Join(rollbackErrs...)
	})
	tx.Add("model.switch.service.install", func() error {
		return withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdService([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID})
		})
	}, nil)
	tx.Add("model.switch.service.start", func() error {
		err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdService([]string{"start", "--vendor", ref.VendorID, "--profile", ref.ProfileID})
		})
		if err == nil {
			return nil
		}
		if isRecoverableSetupServiceStartError(err) {
			if recErr := a.runServiceReconcile(ref, true); recErr == nil {
				return nil
			}
		}
		return err
	}, nil)
	tx.Add("model.switch.claude.apply", func() error {
		expectedActive := expectedActiveRequirement{
			VendorID:   ref.VendorID,
			ProfileID:  ref.ProfileID,
			Generation: strings.TrimSpace(prevActive.ActiveGeneration),
		}
		return withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdClaudeWithExpected([]string{"apply", "--vendor", ref.VendorID, "--profile", ref.ProfileID}, &expectedActive)
		})
	}, func() error {
		rollbackRT, loadErr := a.loadRuntime(ref, false)
		if loadErr != nil {
			return loadErr
		}
		if err := a.ensureProviderCapability(rollbackRT, provider.CapabilityClaude); err != nil {
			return err
		}
		return rollbackRT.Bundle.Claude.Revert(context.Background(), toProviderRuntime(rollbackRT), settingsSnapshotPath, settingsSnapshotSHA)
	})
	tx.Add("model.switch.doctor", func() error {
		return withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdDoctor([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID})
		})
	}, nil)
	tx.Add("model.switch.active.verify", func() error {
		activeNow, err := control.LoadActive(rt.Paths.ActivePath)
		if err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to verify active pointer after model switch", err)
		}
		postState, err := state.Load(rt.Paths.StatePath)
		if err != nil {
			return cberr.Wrap(cberr.ErrStateReadFailed, "failed to read scope state after model switch", err)
		}
		expectedGen := strings.TrimSpace(postState.Claude.AppliedGeneration)
		if expectedGen == "" {
			return cberr.New(cberr.ErrSwitchValidation, "model switch completed without applied generation in state")
		}
		return ensureExpectedActive(activeNow, expectedActiveRequirement{
			VendorID:   ref.VendorID,
			ProfileID:  ref.ProfileID,
			Generation: expectedGen,
		}, "model switch")
	}, nil)

	if err := tx.Run(); err != nil {
		var rbErr *installtx.RollbackError
		if stderrors.As(err, &rbErr) {
			return cberr.Wrap(cberr.ErrRollbackFailed, "model switch failed and rollback was required", err)
		}
		return err
	}

	rt.Logger.Infof("model switch complete scope=%s model=%s", ref.ScopeID(), normalizedModel)
	fmt.Printf("model switched (%s, model=%s)\n", ref.ScopeID(), normalizedModel)
	return nil
}

type preflightCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Blocking bool   `json:"blocking"`
	Detail   string `json:"detail"`
}

type preflightReport struct {
	GeneratedAt    string           `json:"generated_at"`
	FromScope      string           `json:"from_scope"`
	ToScope        string           `json:"to_scope"`
	RequestedModel string           `json:"requested_model,omitempty"`
	ResolvedModel  string           `json:"resolved_model,omitempty"`
	Checks         []preflightCheck `json:"checks"`
	PreflightCmd   string           `json:"preflight_command,omitempty"`
	FailoverCmd    string           `json:"failover_command,omitempty"`
	DoctorCmd      string           `json:"doctor_command,omitempty"`
}

func (r preflightReport) blockingFailures() int {
	count := 0
	for _, c := range r.Checks {
		if c.Blocking && !c.OK {
			count++
		}
	}
	return count
}

func (r preflightReport) warningFailures() int {
	count := 0
	for _, c := range r.Checks {
		if !c.Blocking && !c.OK {
			count++
		}
	}
	return count
}

func (r preflightReport) renderText() string {
	var b strings.Builder
	model := strings.TrimSpace(r.ResolvedModel)
	if model == "" {
		model = "<unspecified>"
	}
	_, _ = fmt.Fprintf(&b, "preflight: from=%s to=%s model=%s\n", r.FromScope, r.ToScope, model)
	for _, c := range r.Checks {
		status := "OK"
		if !c.OK && c.Blocking {
			status = "FAIL"
		} else if !c.OK {
			status = "WARN"
		}
		if c.Blocking {
			_, _ = fmt.Fprintf(&b, "[%s] %s (blocking): %s\n", status, c.Name, c.Detail)
		} else {
			_, _ = fmt.Fprintf(&b, "[%s] %s: %s\n", status, c.Name, c.Detail)
		}
	}
	_, _ = fmt.Fprintf(&b, "blocking failures: %d\n", r.blockingFailures())
	_, _ = fmt.Fprintf(&b, "warnings: %d\n", r.warningFailures())
	if strings.TrimSpace(r.FailoverCmd) != "" {
		_, _ = fmt.Fprintf(&b, "next: %s\n", r.FailoverCmd)
	}
	return strings.TrimSpace(b.String())
}

func (r preflightReport) renderHandoffMarkdown() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "# ccgateway failover handoff\n\n")
	_, _ = fmt.Fprintf(&b, "- generated_at: %s\n", r.GeneratedAt)
	_, _ = fmt.Fprintf(&b, "- from: `%s`\n", r.FromScope)
	_, _ = fmt.Fprintf(&b, "- to: `%s`\n", r.ToScope)
	if strings.TrimSpace(r.ResolvedModel) != "" {
		_, _ = fmt.Fprintf(&b, "- model: `%s`\n", r.ResolvedModel)
	}
	_, _ = fmt.Fprintf(&b, "- blocking_failures: `%d`\n", r.blockingFailures())
	_, _ = fmt.Fprintf(&b, "- warnings: `%d`\n\n", r.warningFailures())

	_, _ = fmt.Fprintf(&b, "## Preflight checks\n\n")
	for _, c := range r.Checks {
		status := "OK"
		if !c.OK && c.Blocking {
			status = "FAIL"
		} else if !c.OK {
			status = "WARN"
		}
		if c.Blocking {
			_, _ = fmt.Fprintf(&b, "- [%s][blocking] %s: %s\n", status, c.Name, c.Detail)
		} else {
			_, _ = fmt.Fprintf(&b, "- [%s] %s: %s\n", status, c.Name, c.Detail)
		}
	}

	_, _ = fmt.Fprintf(&b, "\n## Recommended commands\n\n")
	if strings.TrimSpace(r.PreflightCmd) != "" {
		_, _ = fmt.Fprintf(&b, "```bash\n%s\n```\n\n", r.PreflightCmd)
	}
	if strings.TrimSpace(r.FailoverCmd) != "" {
		_, _ = fmt.Fprintf(&b, "```bash\n%s\n```\n\n", r.FailoverCmd)
	}
	if strings.TrimSpace(r.DoctorCmd) != "" {
		_, _ = fmt.Fprintf(&b, "```bash\n%s\n```\n", r.DoctorCmd)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func (a *application) cmdPreflight(args []string) error {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fromScope := fs.String("from", "", "source scope (vendor:profile)")
	toScope := fs.String("to", "", "target scope (vendor:profile)")
	modelName := fs.String("model", "", "target model name")
	jsonOutput := fs.Bool("json", false, "emit machine-readable output")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, preflightUsage(), "failed to parse preflight flags")
	}
	if err := ensureNoExtraArgs(fs, preflightUsage()); err != nil {
		return err
	}
	if strings.TrimSpace(*fromScope) == "" || strings.TrimSpace(*toScope) == "" {
		return cberr.New(cberr.ErrInvalidArgs, "preflight requires --from and --to")
	}
	fromRef, err := scope.ScopeFromID(*fromScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --from scope id", err)
	}
	toRef, err := scope.ScopeFromID(*toScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --to scope id", err)
	}
	if fromRef.ScopeID() == toRef.ScopeID() {
		return cberr.New(cberr.ErrInvalidArgs, "preflight requires different --from and --to scopes")
	}

	report, err := a.evaluateFailoverPreflight(fromRef, toRef, *modelName)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoded, encErr := json.MarshalIndent(report, "", "  ")
		if encErr != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to encode preflight report", encErr)
		}
		fmt.Println(string(encoded))
	} else {
		fmt.Println(report.renderText())
	}
	if failures := report.blockingFailures(); failures > 0 {
		return cberr.New(cberr.ErrSwitchValidation, fmt.Sprintf("preflight found %d blocking checks", failures))
	}
	return nil
}

func (a *application) cmdHandoff(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, handoffUsage())
	}
	sub := args[0]
	if isHelpArg(sub) {
		fmt.Println(handoffUsage())
		return nil
	}
	if sub != "create" {
		return cberr.New(cberr.ErrInvalidArgs, handoffUsage())
	}

	fs := flag.NewFlagSet("handoff create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fromScope := fs.String("from", "", "source scope (vendor:profile)")
	toScope := fs.String("to", "", "target scope (vendor:profile)")
	modelName := fs.String("model", "", "target model name")
	outputPath := fs.String("output", "", "output file path")
	jsonOutput := fs.Bool("json", false, "emit JSON handoff bundle")
	if err := fs.Parse(args[1:]); err != nil {
		return parseFlagError(err, handoffUsage(), "failed to parse handoff flags")
	}
	if err := ensureNoExtraArgs(fs, handoffUsage()); err != nil {
		return err
	}
	if strings.TrimSpace(*fromScope) == "" || strings.TrimSpace(*toScope) == "" {
		return cberr.New(cberr.ErrInvalidArgs, "handoff create requires --from and --to")
	}
	fromRef, err := scope.ScopeFromID(*fromScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --from scope id", err)
	}
	toRef, err := scope.ScopeFromID(*toScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --to scope id", err)
	}
	if fromRef.ScopeID() == toRef.ScopeID() {
		return cberr.New(cberr.ErrInvalidArgs, "handoff create requires different --from and --to scopes")
	}

	report, err := a.evaluateFailoverPreflight(fromRef, toRef, *modelName)
	if err != nil {
		return err
	}

	var content []byte
	if *jsonOutput {
		encoded, encErr := json.MarshalIndent(report, "", "  ")
		if encErr != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to encode handoff bundle", encErr)
		}
		content = append(encoded, '\n')
	} else {
		content = []byte(report.renderHandoffMarkdown())
	}

	if strings.TrimSpace(*outputPath) != "" {
		path := config.ExpandHome(*outputPath, a.home)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to create handoff output directory", err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return cberr.Wrap(cberr.ErrStateWriteFailed, "failed to write handoff bundle", err)
		}
		fmt.Printf("handoff bundle written: %s\n", path)
		return nil
	}

	fmt.Print(string(content))
	return nil
}

func (a *application) evaluateFailoverPreflight(fromRef, toRef scope.Ref, modelInput string) (preflightReport, error) {
	report := preflightReport{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		FromScope:      fromRef.ScopeID(),
		ToScope:        toRef.ScopeID(),
		RequestedModel: strings.TrimSpace(modelInput),
	}
	addCheck := func(name string, ok, blocking bool, detail string) {
		report.Checks = append(report.Checks, preflightCheck{
			Name:     name,
			OK:       ok,
			Blocking: blocking,
			Detail:   strings.TrimSpace(detail),
		})
	}

	fromRT, err := a.loadRuntime(fromRef, false)
	if err != nil {
		return report, err
	}
	if err := a.enforcePolicy(fromRT, "preflight source"); err != nil {
		addCheck("source policy guard", false, true, err.Error())
	} else {
		addCheck("source policy guard", true, true, "ok")
	}

	active, err := control.LoadActive(fromRT.Paths.ActivePath)
	if err != nil {
		addCheck("source active contract", false, true, fmt.Sprintf("failed to load active pointer: %v", err))
	} else if active.ActiveVendor != fromRef.VendorID || active.ActiveProfile != fromRef.ProfileID {
		activeScope := "<unset>"
		if strings.TrimSpace(active.ActiveVendor) != "" && strings.TrimSpace(active.ActiveProfile) != "" {
			activeScope = fmt.Sprintf("%s:%s", active.ActiveVendor, active.ActiveProfile)
		}
		addCheck(
			"source active contract",
			false,
			true,
			fmt.Sprintf(
				"active scope mismatch (active=%s source=%s); run: ccb use --vendor %s --profile %s",
				activeScope,
				fromRef.ScopeID(),
				fromRef.VendorID,
				fromRef.ProfileID,
			),
		)
	} else if err := ensureActiveSwitchContract(active, fromRT.State, fromRef, "preflight source"); err != nil {
		addCheck("source active contract", false, true, err.Error())
	} else {
		addCheck("source active contract", true, true, fmt.Sprintf("active=%s generation=%s", fromRef.ScopeID(), strings.TrimSpace(active.ActiveGeneration)))
	}

	fromDoctor := doctor.RunScoped(doctor.ScopedInput{
		Scope:  fromRef,
		Paths:  fromRT.Paths,
		Config: fromRT.Config,
		State:  fromRT.State,
		Active: active,
	})
	if check, ok := findCheck(fromDoctor.Checks, "route proof"); ok {
		addCheck("source route proof", check.OK, false, check.Detail)
	}
	if check, ok := findCheck(fromDoctor.Checks, "model proof"); ok {
		addCheck("source model proof", check.OK, false, check.Detail)
	}
	if check, ok := findCheck(fromDoctor.Checks, "tool-call integrity"); ok {
		addCheck("source tool-call integrity", check.OK, false, check.Detail)
	}

	targetPaths := scope.BuildPaths(a.home, a.cwd, toRef)
	targetExists := false
	if _, statErr := os.Stat(targetPaths.ConfigPath); statErr == nil {
		targetExists = true
	} else if !os.IsNotExist(statErr) {
		return report, cberr.Wrap(cberr.ErrInvalidConfig, "failed to inspect target scope config", statErr)
	}

	targetDefaults := config.DefaultForScope(a.home, a.cwd, toRef.VendorID, toRef.ProfileID)
	targetCfg := targetDefaults
	if targetExists {
		toRT, err := a.loadRuntime(toRef, false)
		if err != nil {
			return report, err
		}
		targetCfg = toRT.Config

		if err := a.enforcePolicy(toRT, "preflight target"); err != nil {
			addCheck("target policy guard", false, true, err.Error())
		} else {
			addCheck("target policy guard", true, true, "ok")
		}
		if toRT.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
			addCheck("target runtime mode", false, true, fmt.Sprintf("target runtime_mode=%s is cleanup-only and cannot be failover target", config.RuntimeModeNativeCleanup))
		} else {
			addCheck("target runtime mode", true, true, string(toRT.Config.RuntimeMode))
		}
		if err := ensureScopedSettingsBinding(toRT, "preflight target"); err != nil {
			addCheck("target settings binding", false, true, err.Error())
		} else {
			addCheck("target settings binding", true, true, toRT.Config.SettingsPath)
		}
		if err := a.ensureProviderCapability(toRT, provider.CapabilityClaude); err != nil {
			addCheck("target provider capability", false, true, err.Error())
		} else {
			addCheck("target provider capability", true, true, "claude patcher available")
		}
		if isGatewayProxyMode(toRT) {
			if _, statErr := os.Stat(toRT.Paths.ProxyBinary); statErr != nil {
				addCheck(
					"target gateway artifact",
					false,
					false,
					fmt.Sprintf("proxy binary missing (%s); failover can install it, but cold start may be slower", toRT.Paths.ProxyBinary),
				)
			} else {
				addCheck("target gateway artifact", true, false, toRT.Paths.ProxyBinary)
			}
		}
	} else {
		addCheck("target scope", true, false, "scope does not exist yet; failover will bootstrap target")
		if targetDefaults.RuntimeMode == config.RuntimeModeNativeCleanup {
			addCheck("target runtime mode", false, true, fmt.Sprintf("default runtime_mode=%s is cleanup-only and cannot be failover target", config.RuntimeModeNativeCleanup))
		} else {
			addCheck("target runtime mode", true, true, string(targetDefaults.RuntimeMode))
		}
		targetBundle, ok := a.registry.Get(toRef.VendorID)
		if !ok {
			addCheck("target provider capability", false, true, fmt.Sprintf("provider %s is not registered", toRef.VendorID))
		} else if !targetBundle.Has(provider.CapabilityClaude) || !capabilityImplemented(targetBundle, provider.CapabilityClaude) {
			addCheck("target provider capability", false, true, fmt.Sprintf("provider %s does not support claude capability", toRef.VendorID))
		} else {
			addCheck("target provider capability", true, true, "claude patcher available")
		}
	}

	modelValue := strings.TrimSpace(modelInput)
	if modelValue == "" {
		modelValue = strings.TrimSpace(targetCfg.Model)
		if modelValue == "" {
			modelValue = strings.TrimSpace(targetDefaults.Model)
		}
		addCheck("target model input", true, false, fmt.Sprintf("model omitted; defaulting to %q", modelValue))
	}
	normalizedModel, normErr := modelnorm.NormalizeForVendor(toRef.VendorID, modelValue)
	if normErr != nil {
		addCheck("target model policy", false, true, normErr.Error())
	} else {
		report.ResolvedModel = normalizedModel
		addCheck("target model policy", true, true, normalizedModel)
	}
	if report.ResolvedModel == "" {
		report.ResolvedModel = modelValue
	}

	modelArg := strings.TrimSpace(report.ResolvedModel)
	if modelArg == "" {
		modelArg = "<model>"
	}
	report.PreflightCmd = fmt.Sprintf("ccb preflight --from %s --to %s --model %s", fromRef.ScopeID(), toRef.ScopeID(), modelArg)
	report.FailoverCmd = fmt.Sprintf("ccb failover --from %s --to %s --model %s", fromRef.ScopeID(), toRef.ScopeID(), modelArg)
	report.DoctorCmd = fmt.Sprintf("ccb doctor --vendor %s --profile %s", toRef.VendorID, toRef.ProfileID)
	return report, nil
}

func (a *application) cmdFailover(args []string) error {
	fs := flag.NewFlagSet("failover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fromScope := fs.String("from", "", "source scope (vendor:profile)")
	toScope := fs.String("to", "", "target scope (vendor:profile)")
	modelName := fs.String("model", "", "target model name")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, failoverUsage(), "failed to parse failover flags")
	}
	if err := ensureNoExtraArgs(fs, failoverUsage()); err != nil {
		return err
	}
	if strings.TrimSpace(*fromScope) == "" || strings.TrimSpace(*toScope) == "" {
		return cberr.New(cberr.ErrInvalidArgs, "failover requires --from and --to")
	}
	fromRef, err := scope.ScopeFromID(*fromScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --from scope id", err)
	}
	toRef, err := scope.ScopeFromID(*toScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --to scope id", err)
	}
	if fromRef.ScopeID() == toRef.ScopeID() {
		return cberr.New(cberr.ErrInvalidArgs, "failover requires different --from and --to scopes")
	}
	normalizedModel, err := modelnorm.NormalizeForVendor(toRef.VendorID, *modelName)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "failover requires --model", err)
	}

	fromRT, err := a.loadRuntime(fromRef, false)
	if err != nil {
		return err
	}
	if err := a.enforcePolicy(fromRT, "failover source"); err != nil {
		return err
	}
	if err := a.ensureProviderCapability(fromRT, provider.CapabilityClaude); err != nil {
		return err
	}

	activePath := fromRT.Paths.ActivePath
	prevActive, err := control.LoadActive(activePath)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
	}
	if prevActive.ActiveVendor != fromRef.VendorID || prevActive.ActiveProfile != fromRef.ProfileID {
		activeScope := "<unset>"
		if strings.TrimSpace(prevActive.ActiveVendor) != "" && strings.TrimSpace(prevActive.ActiveProfile) != "" {
			activeScope = fmt.Sprintf("%s:%s", prevActive.ActiveVendor, prevActive.ActiveProfile)
		}
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("failover is source-active only (active=%s source=%s)", activeScope, fromRef.ScopeID()),
		)
	}
	if err := ensureActiveSwitchContract(prevActive, fromRT.State, fromRef, "failover source"); err != nil {
		return err
	}

	toPaths := scope.BuildPaths(a.home, a.cwd, toRef)
	toScopeExisted := false
	if _, statErr := os.Stat(toPaths.ConfigPath); statErr == nil {
		toScopeExisted = true
	} else if !os.IsNotExist(statErr) {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to inspect target scope config", statErr)
	}

	var prevToCfg config.Config
	var prevToState state.State
	if toScopeExisted {
		prevRT, loadErr := a.loadRuntime(toRef, false)
		if loadErr != nil {
			return loadErr
		}
		if err := a.enforcePolicy(prevRT, "failover target"); err != nil {
			return err
		}
		if prevRT.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
			return cberr.New(cberr.ErrInvalidConfig, fmt.Sprintf("target scope runtime_mode=%s is cleanup-only and cannot be failover target", config.RuntimeModeNativeCleanup))
		}
		if err := a.ensureProviderCapability(prevRT, provider.CapabilityClaude); err != nil {
			return err
		}
		if err := ensureScopedSettingsBinding(prevRT, "failover target"); err != nil {
			return err
		}
		prevToCfg = prevRT.Config
		prevToState = prevRT.State
	}

	restorer := &targetScopeRestorer{
		app:            a,
		toRef:          toRef,
		toPaths:        toPaths,
		toScopeExisted: toScopeExisted,
		prevToCfg:      prevToCfg,
		prevToState:    prevToState,
	}

	targetDefaults := config.DefaultForScope(a.home, a.cwd, toRef.VendorID, toRef.ProfileID)
	targetMode := targetDefaults.RuntimeMode
	targetBackend := targetDefaults.GatewayBackend
	targetSettingsLayer := targetDefaults.SettingsLayer
	targetSettingsPath := targetDefaults.SettingsPath
	if toScopeExisted {
		targetMode = prevToCfg.RuntimeMode
		targetBackend = prevToCfg.GatewayBackend
		targetSettingsLayer = prevToCfg.SettingsLayer
		targetSettingsPath = prevToCfg.SettingsPath
	}

	bootstrapArgs := []string{
		"--vendor", toRef.VendorID,
		"--profile", toRef.ProfileID,
		"--runtime-mode", string(targetMode),
		"--model", normalizedModel,
	}
	if strings.TrimSpace(targetBackend) != "" {
		bootstrapArgs = append(bootstrapArgs, "--gateway-backend", targetBackend)
	}
	if strings.TrimSpace(string(targetSettingsLayer)) != "" {
		bootstrapArgs = append(bootstrapArgs, "--settings-layer", string(targetSettingsLayer))
	}
	if strings.TrimSpace(targetSettingsPath) != "" {
		bootstrapArgs = append(bootstrapArgs, "--settings-path", targetSettingsPath)
	}
	if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
		return a.cmdBootstrap(bootstrapArgs)
	}); err != nil {
		if restoreErr := restorer.restore(); restoreErr != nil {
			return cberr.Wrap(cberr.ErrRollbackFailed, "failover target bootstrap failed and rollback failed", fmt.Errorf("bootstrap error: %v; rollback error: %w", err, restoreErr))
		}
		return cberr.Wrap(cberr.ErrSwitchFailed, "failover target bootstrap failed", err)
	}
	restorer.targetBootstrapApplied = true

	toRT, err := a.loadRuntime(toRef, false)
	if err != nil {
		_ = restorer.restore()
		return err
	}
	restorer.rollbackTargetRT = &toRT
	if err := a.enforcePolicy(toRT, "failover target"); err != nil {
		_ = restorer.restore()
		return err
	}
	if toRT.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
		_ = restorer.restore()
		return cberr.New(cberr.ErrInvalidConfig, fmt.Sprintf("target scope runtime_mode=%s is cleanup-only and cannot be failover target", config.RuntimeModeNativeCleanup))
	}
	if err := a.ensureProviderCapability(toRT, provider.CapabilityClaude); err != nil {
		_ = restorer.restore()
		return err
	}
	if err := ensureScopedSettingsBinding(toRT, "failover target"); err != nil {
		_ = restorer.restore()
		return err
	}

	settingsSnapshotPath, settingsSnapshotSHA, err := snapshotSettingsBackup(toRT.Config.SettingsPath, toRT.Paths.SnapshotsDir, "failover")
	if err != nil {
		_ = restorer.restore()
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to snapshot current Claude settings before failover", err)
	}

	rollback := func(cause error) error {
		var rollbackErrs []error
		if err := toRT.Bundle.Claude.Revert(context.Background(), toProviderRuntime(toRT), settingsSnapshotPath, settingsSnapshotSHA); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore settings snapshot: %w", err))
		}
		activeNow, activeErr := control.LoadActive(activePath)
		if activeErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to inspect active pointer during rollback: %w", activeErr))
		} else if activeNow.ActiveVendor == toRef.VendorID && activeNow.ActiveProfile == toRef.ProfileID {
			if err := control.SaveActive(activePath, prevActive); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore active pointer: %w", err))
			}
		}
		if err := restorer.restore(); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		if len(rollbackErrs) > 0 {
			return cberr.Wrap(cberr.ErrRollbackFailed, "failover failed and rollback was required", fmt.Errorf("original error: %v; rollback errors: %w", cause, stderrors.Join(rollbackErrs...)))
		}
		return cause
	}

	if isGatewayProxyMode(toRT) {
		if _, statErr := os.Stat(toRT.Paths.ProxyBinary); statErr != nil {
			if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
				return a.cmdProxy([]string{"install", "--vendor", toRef.VendorID, "--profile", toRef.ProfileID})
			}); err != nil {
				return rollback(cberr.Wrap(cberr.ErrSwitchFailed, "failover target proxy install failed", err))
			}
		}
		if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdAuth([]string{"sync", "--vendor", toRef.VendorID, "--profile", toRef.ProfileID})
		}); err != nil {
			return rollback(cberr.Wrap(cberr.ErrSwitchFailed, "failover target auth sync failed", err))
		}
		if err := a.runServiceReconcile(toRef, true); err != nil {
			return rollback(cberr.Wrap(cberr.ErrSwitchFailed, "failover target service reconcile failed", err))
		}
	}

	expectedSourceActive := expectedActiveRequirement{
		VendorID:   fromRef.VendorID,
		ProfileID:  fromRef.ProfileID,
		Generation: strings.TrimSpace(prevActive.ActiveGeneration),
	}
	if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
		return a.cmdUseWithExpected([]string{"--vendor", toRef.VendorID, "--profile", toRef.ProfileID}, &expectedSourceActive)
	}); err != nil {
		return rollback(cberr.Wrap(cberr.ErrSwitchFailed, "failover switch failed", err))
	}

	if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
		return a.cmdDoctor([]string{"--vendor", toRef.VendorID, "--profile", toRef.ProfileID})
	}); err != nil {
		return rollback(cberr.Wrap(cberr.ErrSwitchValidation, "failover post-switch doctor failed", err))
	}

	activeAfter, err := control.LoadActive(activePath)
	if err != nil {
		return rollback(cberr.Wrap(cberr.ErrInvalidConfig, "failed to verify active pointer after failover", err))
	}
	if activeAfter.ActiveVendor != toRef.VendorID || activeAfter.ActiveProfile != toRef.ProfileID || strings.TrimSpace(activeAfter.ActiveGeneration) == "" {
		return rollback(cberr.New(cberr.ErrSwitchValidation, "failover completed but active pointer is inconsistent"))
	}

	finalRT, err := a.loadRuntime(toRef, false)
	if err != nil {
		return rollback(err)
	}
	if strings.TrimSpace(finalRT.Config.Model) != normalizedModel {
		return rollback(cberr.New(cberr.ErrSwitchValidation, fmt.Sprintf("target model mismatch after failover (want=%q got=%q)", normalizedModel, finalRT.Config.Model)))
	}

	finalRT.Logger.Infof("failover complete from=%s to=%s model=%s", fromRef.ScopeID(), toRef.ScopeID(), normalizedModel)
	fmt.Printf("failover complete (%s -> %s, model=%s)\n", fromRef.ScopeID(), toRef.ScopeID(), normalizedModel)
	return nil
}

func (a *application) cmdScope(args []string) error {
	if len(args) == 0 {
		return cberr.New(cberr.ErrInvalidArgs, scopeUsage())
	}
	sub := args[0]
	if isHelpArg(sub) {
		fmt.Println(scopeUsage())
		return nil
	}
	if sub != "switch" {
		return cberr.New(cberr.ErrInvalidArgs, scopeUsage())
	}
	return a.cmdScopeSwitch(args[1:])
}

func (a *application) cmdScopeSwitch(args []string) error {
	fs := flag.NewFlagSet("scope switch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fromScope := fs.String("from", "", "source scope (vendor:profile)")
	toScope := fs.String("to", "", "target scope (vendor:profile)")
	modelName := fs.String("model", "", "target model name")
	dryRun := fs.Bool("dry-run", false, "validate only, do not execute")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, scopeUsage(), "failed to parse scope switch flags")
	}
	if err := ensureNoExtraArgs(fs, scopeUsage()); err != nil {
		return err
	}
	if strings.TrimSpace(*fromScope) == "" || strings.TrimSpace(*toScope) == "" {
		return cberr.New(cberr.ErrInvalidArgs, "scope switch requires --from and --to")
	}
	fromRef, err := scope.ScopeFromID(*fromScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --from scope id", err)
	}
	toRef, err := scope.ScopeFromID(*toScope)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "invalid --to scope id", err)
	}
	if fromRef.ScopeID() == toRef.ScopeID() {
		return cberr.New(cberr.ErrInvalidArgs, fmt.Sprintf("scope switch requires different --from and --to scopes; for same-scope model change use: ccb model switch --vendor %s --profile %s --model <name>", fromRef.VendorID, fromRef.ProfileID))
	}
	normalizedModel, err := modelnorm.NormalizeForVendor(toRef.VendorID, *modelName)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "scope switch requires --model", err)
	}

	// --- Phase 1: Source validation ---
	fromRT, err := a.loadRuntime(fromRef, false)
	if err != nil {
		return err
	}
	if err := a.enforcePolicy(fromRT, "scope switch source"); err != nil {
		return err
	}
	if err := a.ensureProviderCapability(fromRT, provider.CapabilityClaude); err != nil {
		return err
	}

	activePath := fromRT.Paths.ActivePath
	prevActive, err := control.LoadActive(activePath)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
	}
	if prevActive.ActiveVendor != fromRef.VendorID || prevActive.ActiveProfile != fromRef.ProfileID {
		activeScope := "<unset>"
		if strings.TrimSpace(prevActive.ActiveVendor) != "" && strings.TrimSpace(prevActive.ActiveProfile) != "" {
			activeScope = fmt.Sprintf("%s:%s", prevActive.ActiveVendor, prevActive.ActiveProfile)
		}
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("scope switch is source-active only (active=%s source=%s)", activeScope, fromRef.ScopeID()),
		)
	}
	if err := ensureActiveSwitchContract(prevActive, fromRT.State, fromRef, "scope switch source"); err != nil {
		return err
	}

	// --- Phase 1b: Target pre-check ---
	toPaths := scope.BuildPaths(a.home, a.cwd, toRef)
	toScopeExisted := false
	if _, statErr := os.Stat(toPaths.ConfigPath); statErr == nil {
		toScopeExisted = true
	} else if !os.IsNotExist(statErr) {
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to inspect target scope config", statErr)
	}

	var prevToCfg config.Config
	var prevToState state.State
	if toScopeExisted {
		prevRT, loadErr := a.loadRuntime(toRef, false)
		if loadErr != nil {
			return loadErr
		}
		if err := a.enforcePolicy(prevRT, "scope switch target"); err != nil {
			return err
		}
		if prevRT.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
			return cberr.New(cberr.ErrInvalidConfig, fmt.Sprintf("target scope runtime_mode=%s is cleanup-only and cannot be scope switch target", config.RuntimeModeNativeCleanup))
		}
		if err := a.ensureProviderCapability(prevRT, provider.CapabilityClaude); err != nil {
			return err
		}
		if err := ensureScopedSettingsBinding(prevRT, "scope switch target"); err != nil {
			return err
		}
		prevToCfg = prevRT.Config
		prevToState = prevRT.State
	}

	// --- Phase 2: Dry-run gate ---
	if *dryRun {
		targetStatus := "will be created"
		if toScopeExisted {
			targetStatus = "exists"
		}
		fmt.Printf("scope switch dry-run: from=%s to=%s model=%s\n", fromRef.ScopeID(), toRef.ScopeID(), normalizedModel)
		fmt.Printf("[OK] source active contract (gen=%s)\n", strings.TrimSpace(prevActive.ActiveGeneration))
		fmt.Printf("[OK] source policy\n")
		fmt.Printf("[OK] target pre-check (%s)\n", targetStatus)
		fmt.Printf("[OK] model normalization (%s)\n", normalizedModel)
		fmt.Printf("dry-run passed (pre-validation only) — no changes made\n")
		fmt.Printf("note: proxy, auth, and service checks run only during actual execution\n")
		return nil
	}

	// --- Phase 3: Target setup ---
	restorer := &targetScopeRestorer{
		app:            a,
		toRef:          toRef,
		toPaths:        toPaths,
		toScopeExisted: toScopeExisted,
		prevToCfg:      prevToCfg,
		prevToState:    prevToState,
	}

	targetDefaults := config.DefaultForScope(a.home, a.cwd, toRef.VendorID, toRef.ProfileID)
	targetMode := targetDefaults.RuntimeMode
	targetBackend := targetDefaults.GatewayBackend
	targetSettingsLayer := targetDefaults.SettingsLayer
	targetSettingsPath := targetDefaults.SettingsPath
	if toScopeExisted {
		targetMode = prevToCfg.RuntimeMode
		targetBackend = prevToCfg.GatewayBackend
		targetSettingsLayer = prevToCfg.SettingsLayer
		targetSettingsPath = prevToCfg.SettingsPath
	}

	bootstrapArgs := []string{
		"--vendor", toRef.VendorID,
		"--profile", toRef.ProfileID,
		"--runtime-mode", string(targetMode),
		"--model", normalizedModel,
	}
	if strings.TrimSpace(targetBackend) != "" {
		bootstrapArgs = append(bootstrapArgs, "--gateway-backend", targetBackend)
	}
	if strings.TrimSpace(string(targetSettingsLayer)) != "" {
		bootstrapArgs = append(bootstrapArgs, "--settings-layer", string(targetSettingsLayer))
	}
	if strings.TrimSpace(targetSettingsPath) != "" {
		bootstrapArgs = append(bootstrapArgs, "--settings-path", targetSettingsPath)
	}

	if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
		return a.cmdBootstrap(bootstrapArgs)
	}); err != nil {
		if restoreErr := restorer.restore(); restoreErr != nil {
			return cberr.Wrap(cberr.ErrRollbackFailed, "scope switch target bootstrap failed and rollback failed", fmt.Errorf("bootstrap error: %v; rollback error: %w", err, restoreErr))
		}
		return cberr.Wrap(cberr.ErrSwitchFailed, "scope switch target bootstrap failed", err)
	}
	restorer.targetBootstrapApplied = true

	toRT, err := a.loadRuntime(toRef, false)
	if err != nil {
		_ = restorer.restore()
		return err
	}
	restorer.rollbackTargetRT = &toRT

	if err := a.enforcePolicy(toRT, "scope switch target"); err != nil {
		_ = restorer.restore()
		return err
	}
	if toRT.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
		_ = restorer.restore()
		return cberr.New(cberr.ErrInvalidConfig, fmt.Sprintf("target scope runtime_mode=%s is cleanup-only and cannot be scope switch target", config.RuntimeModeNativeCleanup))
	}
	if err := a.ensureProviderCapability(toRT, provider.CapabilityClaude); err != nil {
		_ = restorer.restore()
		return err
	}
	if err := ensureScopedSettingsBinding(toRT, "scope switch target"); err != nil {
		_ = restorer.restore()
		return err
	}

	// --- Phase 4: Transaction ---
	settingsSnapshotPath, settingsSnapshotSHA, err := snapshotSettingsBackup(toRT.Config.SettingsPath, toRT.Paths.SnapshotsDir, "scope-switch")
	if err != nil {
		_ = restorer.restore()
		return cberr.Wrap(cberr.ErrInvalidConfig, "failed to snapshot current Claude settings before scope switch", err)
	}

	tx := installtx.New(func(step string) error {
		current, loadErr := state.Load(toRT.Paths.StatePath)
		if loadErr != nil {
			return loadErr
		}
		current.LastStep = step
		if saveErr := state.Save(toRT.Paths.StatePath, current); saveErr != nil {
			return saveErr
		}
		toRT.State = current
		return nil
	})

	tx.Add("scope.switch.proxy.setup", func() error {
		if !isGatewayProxyMode(toRT) {
			return nil
		}
		if _, statErr := os.Stat(toRT.Paths.ProxyBinary); statErr != nil {
			if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
				return a.cmdProxy([]string{"install", "--vendor", toRef.VendorID, "--profile", toRef.ProfileID})
			}); err != nil {
				return cberr.Wrap(cberr.ErrSwitchFailed, "scope switch target proxy install failed", err)
			}
		}
		if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdAuth([]string{"sync", "--vendor", toRef.VendorID, "--profile", toRef.ProfileID})
		}); err != nil {
			return cberr.Wrap(cberr.ErrSwitchFailed, "scope switch target auth sync failed", err)
		}
		if err := a.runServiceReconcile(toRef, true); err != nil {
			return cberr.Wrap(cberr.ErrSwitchFailed, "scope switch target service reconcile failed", err)
		}
		return nil
	}, nil)

	tx.Add("scope.switch.activate", func() error {
		expectedSourceActive := expectedActiveRequirement{
			VendorID:   fromRef.VendorID,
			ProfileID:  fromRef.ProfileID,
			Generation: strings.TrimSpace(prevActive.ActiveGeneration),
		}
		return withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdUseWithExpected([]string{"--vendor", toRef.VendorID, "--profile", toRef.ProfileID}, &expectedSourceActive)
		})
	}, func() error {
		var rollbackErrs []error
		if err := toRT.Bundle.Claude.Revert(context.Background(), toProviderRuntime(toRT), settingsSnapshotPath, settingsSnapshotSHA); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore settings snapshot: %w", err))
		}
		activeNow, activeErr := control.LoadActive(activePath)
		if activeErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to inspect active pointer during rollback: %w", activeErr))
		} else if activeNow.ActiveVendor == toRef.VendorID && activeNow.ActiveProfile == toRef.ProfileID {
			if err := control.SaveActive(activePath, prevActive); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("failed to restore active pointer: %w", err))
			}
		}
		if err := restorer.restore(); err != nil {
			rollbackErrs = append(rollbackErrs, err)
		}
		return stderrors.Join(rollbackErrs...)
	})

	tx.Add("scope.switch.doctor", func() error {
		return withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return a.cmdDoctor([]string{"--vendor", toRef.VendorID, "--profile", toRef.ProfileID})
		})
	}, nil)

	tx.Add("scope.switch.verify", func() error {
		activeAfter, err := control.LoadActive(activePath)
		if err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to verify active pointer after scope switch", err)
		}
		if activeAfter.ActiveVendor != toRef.VendorID || activeAfter.ActiveProfile != toRef.ProfileID || strings.TrimSpace(activeAfter.ActiveGeneration) == "" {
			return cberr.New(cberr.ErrSwitchValidation, "scope switch completed but active pointer is inconsistent")
		}
		finalRT, err := a.loadRuntime(toRef, false)
		if err != nil {
			return err
		}
		if strings.TrimSpace(finalRT.Config.Model) != normalizedModel {
			return cberr.New(cberr.ErrSwitchValidation, fmt.Sprintf("target model mismatch after scope switch (want=%q got=%q)", normalizedModel, finalRT.Config.Model))
		}
		return nil
	}, nil)

	if err := tx.Run(); err != nil {
		var rbErr *installtx.RollbackError
		if stderrors.As(err, &rbErr) {
			return cberr.Wrap(cberr.ErrRollbackFailed, "scope switch failed and rollback was required", err)
		}
		return err
	}

	toRT.Logger.Infof("scope switch complete from=%s to=%s model=%s", fromRef.ScopeID(), toRef.ScopeID(), normalizedModel)
	fmt.Printf("scope switched (%s -> %s, model=%s)\n", fromRef.ScopeID(), toRef.ScopeID(), normalizedModel)
	return nil
}

func (a *application) cmdUse(args []string) error {
	return a.cmdUseWithExpected(args, nil)
}

func (a *application) cmdUseWithExpected(args []string, expected *expectedActiveRequirement) error {
	fs := flag.NewFlagSet("use", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, useUsage(), "failed to parse use flags")
	}
	if err := ensureNoExtraArgs(fs, useUsage()); err != nil {
		return err
	}
	ref, err := parseRequiredScope(*vendorID, *profileID)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "use requires --vendor and --profile", err)
	}

	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}
	if err := a.enforcePolicy(rt, "use"); err != nil {
		return err
	}
	if err := ensureScopedSettingsBinding(rt, "use"); err != nil {
		return err
	}
	if rt.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
		return cberr.New(cberr.ErrCapabilityMissing, "runtime_mode=native-cleanup is cleanup-only and cannot be activated via 'ccb use'")
	}
	if err := a.ensureProviderCapability(rt, provider.CapabilityClaude); err != nil {
		return err
	}
	needsGatewayHealth := isGatewayProxyMode(rt)
	if needsGatewayHealth {
		if err := a.ensureBackendCapability(rt, backend.CapabilityHealth); err != nil {
			return err
		}
	}
	preservedSnapshotPath, preservedSnapshotSHA, preserveSnapshot := reusableClaudeSnapshot(rt.State)

	if err := control.WithSwitchLock(rt.Paths.SwitchLock, func() error {
		if err := settingsguard.CheckEffectiveOverride(rt.Paths, rt.Config); err != nil {
			return cberr.Wrap(cberr.ErrConfigOverridden, "effective settings would override active scope", err)
		}
		if needsGatewayHealth {
			if err := rt.Backend.Health.Check(context.Background(), toBackendRuntime(rt)); err != nil {
				return cberr.Wrap(cberr.ErrSwitchValidation, "target scope healthcheck failed", err)
			}
		}

		generation := control.NextGeneration()
		prevActive, err := control.LoadActive(rt.Paths.ActivePath)
		if err != nil {
			return cberr.Wrap(cberr.ErrSwitchFailed, "failed to load active pointer", err)
		}
		if expected != nil {
			if err := ensureExpectedActive(prevActive, *expected, "use"); err != nil {
				return err
			}
		}

		var applySnapshot string
		var applySHA string
		res, err := rt.Bundle.Claude.Apply(context.Background(), toProviderRuntime(rt), generation)
		if err != nil {
			return cberr.Wrap(cberr.ErrSwitchFailed, "failed to apply target settings", err)
		}
		applySnapshot = res.SnapshotPath
		applySHA = res.SnapshotSHA256

		if needsGatewayHealth {
			if err := rt.Backend.Health.Check(context.Background(), toBackendRuntime(rt)); err != nil {
				if revertErr := rt.Bundle.Claude.Revert(context.Background(), toProviderRuntime(rt), applySnapshot, applySHA); revertErr != nil {
					return cberr.Wrap(cberr.ErrRollbackFailed, "target scope failed healthcheck after settings apply and rollback revert failed", fmt.Errorf("healthcheck failed: %v; revert failed: %w", err, revertErr))
				}
				return cberr.Wrap(cberr.ErrSwitchValidation, "target scope failed healthcheck after settings apply", err)
			}
		}

		active := control.ActivePointer{
			SchemaVersion:    1,
			ActiveVendor:     ref.VendorID,
			ActiveProfile:    ref.ProfileID,
			ActiveGeneration: generation,
		}
		if err := control.SaveActive(rt.Paths.ActivePath, active); err != nil {
			revertErr := rt.Bundle.Claude.Revert(context.Background(), toProviderRuntime(rt), applySnapshot, applySHA)
			restoreErr := control.SaveActive(rt.Paths.ActivePath, prevActive)
			if revertErr != nil || restoreErr != nil {
				return cberr.Wrap(cberr.ErrRollbackFailed, "failed to commit active pointer and rollback failed", fmt.Errorf("save active failed: %v; revert failed: %v; restore active failed: %v", err, revertErr, restoreErr))
			}
			return cberr.Wrap(cberr.ErrSwitchFailed, "failed to commit active pointer", err)
		}

		rt.State.Claude.Applied = true
		if preserveSnapshot {
			rt.State.Claude.SnapshotPath = preservedSnapshotPath
			rt.State.Claude.SnapshotSHA256 = preservedSnapshotSHA
		} else {
			rt.State.Claude.SnapshotPath = applySnapshot
			rt.State.Claude.SnapshotSHA256 = applySHA
		}
		rt.State.Claude.AppliedGeneration = generation
		if err := state.Save(rt.Paths.StatePath, rt.State); err != nil {
			revertErr := rt.Bundle.Claude.Revert(context.Background(), toProviderRuntime(rt), applySnapshot, applySHA)
			restoreErr := control.SaveActive(rt.Paths.ActivePath, prevActive)
			if revertErr != nil || restoreErr != nil {
				return cberr.Wrap(cberr.ErrRollbackFailed, "failed to update scope state and rollback failed", fmt.Errorf("state save failed: %v; revert failed: %v; restore active failed: %v", err, revertErr, restoreErr))
			}
			return cberr.Wrap(cberr.ErrSwitchFailed, "failed to update scope state", err)
		}
		return nil
	}); err != nil {
		if cberr.Code(err) != cberr.ErrUnknown {
			return err
		}
		return cberr.Wrap(cberr.ErrSwitchLockFailed, "switch failed", err)
	}

	rt.Logger.Infof("active scope switched to %s", ref.ScopeID())
	fmt.Printf("active scope switched to %s\n", ref.ScopeID())
	return nil
}

func (a *application) cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vendorID := fs.String("vendor", "", "vendor id")
	profileID := fs.String("profile", "", "profile id")
	purge := fs.Bool("purge", false, "remove all files for scope")
	if err := fs.Parse(args); err != nil {
		return parseFlagError(err, uninstallUsage(), "failed to parse uninstall flags")
	}
	if err := ensureNoExtraArgs(fs, uninstallUsage()); err != nil {
		return err
	}
	ref, err := parseRequiredScope(*vendorID, *profileID)
	if err != nil {
		return cberr.Wrap(cberr.ErrInvalidArgs, "uninstall requires --vendor and --profile", err)
	}
	rt, err := a.loadRuntime(ref, false)
	if err != nil {
		return err
	}

	proxyLabel, syncLabel := serviceLabelsForRuntime(rt, a.username)
	proxyPlistPath, syncPlistPath := launchAgentPlistPaths(rt.Paths, proxyLabel, syncLabel)
	mgr := launchd.NewManager()
	if err := mgr.RemoveAgents(proxyLabel, syncLabel, proxyPlistPath, syncPlistPath); err != nil {
		return cberr.Wrap(cberr.ErrLaunchctlFailed, "failed to remove launch agents", err)
	}

	if rt.State.Claude.Applied {
		if err := control.WithSwitchLock(rt.Paths.SwitchLock, func() error {
			active, err := control.LoadActive(rt.Paths.ActivePath)
			if err != nil {
				return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
			}
			if !allowSettingsMutation(active, ref, rt.State.Claude.AppliedGeneration) {
				if active.ActiveVendor == ref.VendorID && active.ActiveProfile == ref.ProfileID {
					return cberr.New(cberr.ErrGenerationMismatch, "unsafe uninstall blocked: active scope generation does not match applied generation")
				}
				rt.Logger.Infof(
					"skip claude revert during uninstall for inactive/mismatched scope=%s active=%s:%s/%s state_generation=%s",
					ref.ScopeID(),
					active.ActiveVendor,
					active.ActiveProfile,
					active.ActiveGeneration,
					rt.State.Claude.AppliedGeneration,
				)
				return nil
			}
			return rt.Bundle.Claude.Revert(context.Background(), toProviderRuntime(rt), rt.State.Claude.SnapshotPath, rt.State.Claude.SnapshotSHA256)
		}); err != nil {
			if cberr.Code(err) != cberr.ErrUnknown {
				return err
			}
			return cberr.Wrap(cberr.ErrSwitchLockFailed, "failed to coordinate claude revert during uninstall", err)
		}
	}

	if *purge {
		if err := control.WithSwitchLock(rt.Paths.SwitchLock, func() error {
			if err := control.ReleaseScopePort(rt.Paths.PortsPath, ref.ScopeID()); err != nil {
				return err
			}
			active, err := control.LoadActive(rt.Paths.ActivePath)
			if err != nil {
				return cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active pointer", err)
			}
			if active.ActiveVendor == ref.VendorID && active.ActiveProfile == ref.ProfileID {
				active.ActiveVendor = ""
				active.ActiveProfile = ""
				active.ActiveGeneration = ""
				return control.SaveActive(rt.Paths.ActivePath, active)
			}
			return nil
		}); err != nil {
			return cberr.Wrap(cberr.ErrSwitchLockFailed, "failed to update global state during purge", err)
		}
		if err := os.RemoveAll(rt.Paths.ScopeDir); err != nil {
			return cberr.Wrap(cberr.ErrInvalidConfig, "failed to remove scope directory", err)
		}
		fmt.Printf("uninstall complete (purged: %s)\n", ref.ScopeID())
		return nil
	}

	rt.State.Claude.Applied = false
	rt.State.Claude.SnapshotPath = ""
	rt.State.Claude.SnapshotSHA256 = ""
	rt.State.Claude.AppliedGeneration = ""
	rt.State.Service.Running = false
	if err := state.Save(rt.Paths.StatePath, rt.State); err != nil {
		return cberr.Wrap(cberr.ErrStateWriteFailed, "failed to persist state", err)
	}
	fmt.Printf("uninstall complete (%s)\n", ref.ScopeID())
	return nil
}

func (a *application) loadRuntime(ref scope.Ref, create bool) (runtime, error) {
	paths := scope.BuildPaths(a.home, a.cwd, ref)
	if create {
		if err := ensureDirs(paths); err != nil {
			return runtime{}, cberr.Wrap(cberr.ErrInvalidConfig, "failed to prepare directories", err)
		}
	}
	if !create {
		if _, err := os.Stat(paths.ConfigPath); err != nil {
			if os.IsNotExist(err) {
				return runtime{}, cberr.New(cberr.ErrScopeNotFound, "scope not found; run bootstrap first")
			}
			return runtime{}, cberr.Wrap(cberr.ErrInvalidConfig, "failed to read scope config", err)
		}
	}

	defaults := config.DefaultForScope(a.home, a.cwd, ref.VendorID, ref.ProfileID)
	defaults.AuthSource = paths.AuthSource
	defaults.AuthTarget = paths.AuthTarget
	defaults.SettingsPath = settingsguard.ResolveSettingsPath(paths, defaults)

	cfg, err := config.LoadWithDefault(paths.ConfigPath, defaults)
	if err != nil {
		return runtime{}, cberr.Wrap(cberr.ErrInvalidConfig, "failed to load config", err)
	}
	if cfg.AuthSource == "" {
		cfg.AuthSource = defaults.AuthSource
	}
	if cfg.AuthTarget == "" {
		cfg.AuthTarget = defaults.AuthTarget
	}
	if cfg.SettingsPath == "" {
		cfg.SettingsPath = settingsguard.ResolveSettingsPath(paths, cfg)
	}
	if cfg.GatewayBackend == "" {
		cfg.GatewayBackend = config.DefaultGatewayBackend
	}
	if strings.TrimSpace(cfg.PolicyMode) == "" {
		cfg.PolicyMode = policyguard.DefaultModeForVendor(ref.VendorID)
	}

	st, err := state.Load(paths.StatePath)
	if err != nil {
		return runtime{}, cberr.Wrap(cberr.ErrStateReadFailed, "failed to load state", err)
	}
	st.Scope.VendorID = ref.VendorID
	st.Scope.ProfileID = ref.ProfileID
	st.RuntimeMode = string(cfg.RuntimeMode)
	proxyLabel, syncLabel := ref.Labels(a.username)
	if st.Service.ProxyLabel == "" {
		st.Service.ProxyLabel = proxyLabel
	}
	if st.Service.SyncLabel == "" {
		st.Service.SyncLabel = syncLabel
	}
	if st.Proxy.BinaryPath == "" {
		st.Proxy.BinaryPath = paths.ProxyBinary
	}
	st.Backend.ID = cfg.GatewayBackend
	if st.Backend.BinaryPath == "" {
		st.Backend.BinaryPath = paths.ProxyBinary
	}

	if a.registry == nil {
		a.registry = providers.DefaultRegistry()
	}
	bundle, ok := a.registry.Get(ref.VendorID)
	if !ok {
		return runtime{}, cberr.New(cberr.ErrProviderMissing, "provider not registered for scope")
	}
	if a.backendRegistry == nil {
		a.backendRegistry = backends.DefaultRegistry()
	}
	requireBackend := cfg.RuntimeMode == config.RuntimeModeGateway && cfg.ProxyEnabled
	backendBundle, ok := a.backendRegistry.Get(cfg.GatewayBackend)
	if !ok {
		if !create && requireBackend {
			return runtime{}, cberr.New(cberr.ErrBackendMissing, fmt.Sprintf("backend %s not registered for scope", cfg.GatewayBackend))
		}
		backendBundle = backend.Bundle{ID: cfg.GatewayBackend}
	}
	logger, err := logx.New(paths.AppLogPath)
	if err != nil {
		return runtime{}, cberr.Wrap(cberr.ErrInvalidConfig, "failed to initialize logger", err)
	}

	rt := runtime{Ref: ref, Paths: paths, Config: cfg, State: st, Bundle: bundle, Backend: backendBundle, Logger: logger}
	if !create {
		if err := a.ensureModeSupported(rt); err != nil {
			return runtime{}, err
		}
	}
	return rt, nil
}

func (a *application) ensureModeSupported(rt runtime) error {
	if !rt.Bundle.SupportsMode(string(rt.Config.RuntimeMode)) {
		return cberr.New(cberr.ErrCapabilityMissing, fmt.Sprintf("provider %s does not support runtime mode %s", rt.Ref.VendorID, rt.Config.RuntimeMode))
	}
	if rt.Config.RuntimeMode == config.RuntimeModeGateway && rt.Config.ProxyEnabled {
		if strings.TrimSpace(rt.Config.GatewayBackend) == "" {
			return cberr.New(cberr.ErrInvalidConfig, "gateway_backend is required in runtime_mode=gateway")
		}
		if !rt.Backend.Has(backend.CapabilityProxy) || !backendCapabilityImplemented(rt.Backend, backend.CapabilityProxy) {
			return cberr.New(cberr.ErrCapabilityMissing, fmt.Sprintf("backend %s lacks proxy capability", rt.Config.GatewayBackend))
		}
	}
	return nil
}

func (a *application) ensureProviderCapability(rt runtime, cap provider.Capability) error {
	if !rt.Bundle.Has(cap) || !capabilityImplemented(rt.Bundle, cap) {
		return cberr.New(cberr.ErrCapabilityMissing, fmt.Sprintf("provider %s does not support %s", rt.Ref.VendorID, cap))
	}
	return nil
}

func (a *application) ensureBackendCapability(rt runtime, cap backend.Capability) error {
	if !rt.Backend.Has(cap) || !backendCapabilityImplemented(rt.Backend, cap) {
		return cberr.New(cberr.ErrCapabilityMissing, fmt.Sprintf("backend %s does not support %s", rt.Config.GatewayBackend, cap))
	}
	return nil
}

func (a *application) enforcePolicy(rt runtime, command string) error {
	ev := policyguard.Evaluate(rt.Paths, rt.Config)
	if ev.Mode != policyguard.ModeStrict || len(ev.Violations) == 0 {
		return nil
	}
	return cberr.New(
		cberr.ErrPolicyViolation,
		fmt.Sprintf("%s blocked by policy guard (mode=%s): %s", command, ev.Mode, ev.Violations[0]),
	)
}

func (a *application) resolveScope(vendorID, profileID string, activeFlag, readOnly bool) (scope.Ref, error) {
	if strings.TrimSpace(vendorID) != "" || strings.TrimSpace(profileID) != "" {
		return parseRequiredScope(vendorID, profileID)
	}
	if !readOnly && !activeFlag {
		return scope.Ref{}, cberr.New(cberr.ErrInvalidArgs, "--vendor and --profile are required")
	}
	paths := scope.BuildPaths(a.home, a.cwd, scope.MustRef("codex", "default"))
	active, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		return scope.Ref{}, cberr.Wrap(cberr.ErrInvalidConfig, "failed to load active scope", err)
	}
	if active.ActiveVendor == "" || active.ActiveProfile == "" {
		return scope.Ref{}, cberr.New(cberr.ErrScopeNotFound, "active scope is not set")
	}
	return scope.NewRef(active.ActiveVendor, active.ActiveProfile)
}

func parseRequiredScope(vendorID, profileID string) (scope.Ref, error) {
	if strings.TrimSpace(vendorID) == "" || strings.TrimSpace(profileID) == "" {
		return scope.Ref{}, fmt.Errorf("missing scope flags")
	}
	return scope.NewRef(vendorID, profileID)
}

type setupInteractiveInput struct {
	VendorID       string
	ProfileID      string
	RuntimeMode    string
	GatewayBackend string
	SettingsLayer  string
	Model          string
}

func (a *application) collectSetupInteractiveInputs(input setupInteractiveInput) (setupInteractiveInput, error) {
	reader := bufio.NewReader(os.Stdin)
	out := os.Stdout

	vendor, err := promptWithDefault(reader, out, "vendor", nonEmptyOrDefault(strings.TrimSpace(input.VendorID), "codex"))
	if err != nil {
		return input, err
	}
	profile, err := promptWithDefault(reader, out, "profile", nonEmptyOrDefault(strings.TrimSpace(input.ProfileID), "default"))
	if err != nil {
		return input, err
	}
	defaults := config.DefaultForScope(a.home, a.cwd, vendor, profile)

	runtimeValue, err := promptWithDefault(reader, out, "runtime-mode", nonEmptyOrDefault(strings.TrimSpace(input.RuntimeMode), string(defaults.RuntimeMode)))
	if err != nil {
		return input, err
	}
	parsedRuntime, ok := config.ParseRuntimeMode(runtimeValue)
	if !ok {
		return input, fmt.Errorf("runtime-mode must be gateway|native-cleanup|native-direct")
	}

	settingsLayerValue, err := promptWithDefault(reader, out, "settings-layer", nonEmptyOrDefault(strings.TrimSpace(input.SettingsLayer), string(defaults.SettingsLayer)))
	if err != nil {
		return input, err
	}
	switch config.SettingsLayer(settingsLayerValue) {
	case config.SettingsLayerUser, config.SettingsLayerProject, config.SettingsLayerLocal:
	default:
		return input, fmt.Errorf("settings-layer must be user|project|local")
	}

	modelValue, err := promptWithDefault(reader, out, "model", nonEmptyOrDefault(strings.TrimSpace(input.Model), defaults.Model))
	if err != nil {
		return input, err
	}

	gatewayBackendValue := strings.TrimSpace(input.GatewayBackend)
	if parsedRuntime == config.RuntimeModeGateway {
		gatewayBackendValue, err = promptWithDefault(reader, out, "gateway-backend", nonEmptyOrDefault(gatewayBackendValue, defaults.GatewayBackend))
		if err != nil {
			return input, err
		}
	}

	return setupInteractiveInput{
		VendorID:       vendor,
		ProfileID:      profile,
		RuntimeMode:    string(parsedRuntime),
		GatewayBackend: gatewayBackendValue,
		SettingsLayer:  settingsLayerValue,
		Model:          modelValue,
	}, nil
}

func promptWithDefault(reader *bufio.Reader, out io.Writer, key, defaultValue string) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("reader is nil")
	}
	if out == nil {
		out = os.Stdout
	}
	defaultValue = strings.TrimSpace(defaultValue)
	if _, err := fmt.Fprintf(out, "%s [%s]: ", strings.TrimSpace(key), defaultValue); err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = defaultValue
	}
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func toProviderRuntime(rt runtime) provider.ScopeRuntime {
	return provider.ScopeRuntime{
		Ref:    rt.Ref,
		Paths:  rt.Paths,
		Config: rt.Config,
	}
}

func toBackendRuntime(rt runtime) backend.Runtime {
	return backend.Runtime{
		Ref:    rt.Ref,
		Paths:  rt.Paths,
		Config: rt.Config,
	}
}

func ensureDirs(paths scope.Paths) error {
	for _, p := range []string{
		paths.BaseDir,
		paths.GlobalDir,
		filepath.Dir(paths.SwitchLock),
		filepath.Dir(paths.ActivePath),
		filepath.Dir(paths.PortsPath),
		paths.ScopeDir,
		paths.AuthDir,
		paths.LogsDir,
		paths.SnapshotsDir,
		paths.ProxyDir,
		paths.LaunchdDir,
		paths.LaunchAgentDir,
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func usage() string {
	return strings.TrimSpace(fmt.Sprintf(`Usage:
  %s
  %s
  %s
  %s
  ccb service install --vendor <v> --profile <p>
  ccb service start --vendor <v> --profile <p>
  ccb service stop --vendor <v> --profile <p>
  ccb service status [--vendor <v> --profile <p> | --active]
  %s
  ccb gateway serve --config <path>
  ccb claude apply --vendor <v> --profile <p>
  ccb claude revert --vendor <v> --profile <p>
  %s
  %s
  %s
  %s
  %s
  %s
  %s
  %s`,
		bootstrapUsage(),
		setupUsage(),
		proxyInstallUsage(),
		authSyncUsage(),
		statusUsage(),
		modelUsage(),
		scopeUsage(),
		failoverUsage(),
		useUsage(),
		doctorUsage(),
		preflightUsage(),
		handoffUsage(),
		uninstallUsage(),
	))
}

func bootstrapUsage() string {
	return "ccb bootstrap --vendor <v> --profile <p> [--runtime-mode gateway|native-cleanup|native-direct] [--gateway-backend <id>] [--settings-layer user|project|local] [--settings-path <path>] [--model <name>] [--port <n>] [--auth-source <path>] [--auth-target <path>] [--proxy-enabled true|false]"
}

func setupUsage() string {
	return "ccb setup [--interactive] --vendor <v> --profile <p> [--runtime-mode gateway|native-cleanup|native-direct] [--gateway-backend <id>] [--settings-layer user|project|local] [--settings-path <path>] [--model <name>] [--proxy-version <tag|latest>] [--skip-claude-apply] [--skip-doctor]"
}

func proxyInstallUsage() string {
	return "ccb proxy install --vendor <v> --profile <p> [--version <tag|latest>]"
}

func authSyncUsage() string {
	return "ccb auth sync --vendor <v> --profile <p>"
}

func serviceUsage() string {
	return strings.TrimSpace(`ccb service install --vendor <v> --profile <p>
ccb service start --vendor <v> --profile <p>
ccb service reconcile --vendor <v> --profile <p>
ccb service stop --vendor <v> --profile <p>
ccb service status [--vendor <v> --profile <p> | --active]`)
}

func gatewayUsage() string {
	return "ccb gateway serve --config <path>"
}

func claudeUsage() string {
	return "ccb claude <apply|revert> --vendor <v> --profile <p>"
}

func doctorUsage() string {
	return "ccb doctor [--vendor <v> --profile <p> | --active] [--clear-error-history] [--verbose]"
}

func statusUsage() string {
	return "ccb status [--vendor <v> --profile <p> | --active] [--since <duration>] [--json] (alias: ccb ccb-status ...)"
}

func modelUsage() string {
	return "ccb model switch --vendor <v> --profile <p> --model <name>"
}

func preflightUsage() string {
	return "ccb preflight --from <vendor:profile> --to <vendor:profile> [--model <name>] [--json]"
}

func handoffUsage() string {
	return "ccb handoff create --from <vendor:profile> --to <vendor:profile> [--model <name>] [--output <path>] [--json]"
}

func scopeUsage() string {
	return "ccb scope switch --from <vendor:profile> --to <vendor:profile> --model <name> [--dry-run]"
}

func failoverUsage() string {
	return "ccb failover --from <vendor:profile> --to <vendor:profile> --model <name>"
}

func useUsage() string {
	return "ccb use --vendor <v> --profile <p>"
}

func uninstallUsage() string {
	return "ccb uninstall --vendor <v> --profile <p> [--purge]"
}

func parseFlagError(err error, usageText, parseMessage string) error {
	if err == nil {
		return nil
	}
	if stderrors.Is(err, flag.ErrHelp) {
		fmt.Println(usageText)
		return nil
	}
	return cberr.Wrap(cberr.ErrInvalidArgs, parseMessage, err)
}

func ensureNoExtraArgs(fs *flag.FlagSet, usageText string) error {
	if fs == nil || fs.NArg() == 0 {
		return nil
	}
	return cberr.New(cberr.ErrInvalidArgs, fmt.Sprintf("%s\nunexpected arguments: %s", usageText, strings.Join(fs.Args(), " ")))
}

func (a *application) logFailure(args []string, err error) {
	if a == nil || err == nil {
		return
	}
	logPath := filepath.Join(a.home, ".ccgateway", "logs", "app.log")
	if ref, ok := a.scopeForLogging(args); ok {
		logPath = scope.BuildPaths(a.home, a.cwd, ref).AppLogPath
	}
	logger, logErr := logx.New(logPath)
	if logErr != nil {
		return
	}
	code := cberr.Code(err)
	if code == "" {
		code = cberr.ErrUnknown
	}
	logger.Errorf(code, err, "command=%s", strings.Join(args, " "))
}

func (a *application) scopeForLogging(args []string) (scope.Ref, bool) {
	if a == nil {
		return scope.Ref{}, false
	}
	var vendorID string
	var profileID string
	var fromScope string
	activeFlag := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--vendor":
			if i+1 < len(args) {
				vendorID = strings.TrimSpace(args[i+1])
				i++
			}
		case "--profile":
			if i+1 < len(args) {
				profileID = strings.TrimSpace(args[i+1])
				i++
			}
		case "--active":
			activeFlag = true
		case "--from":
			if i+1 < len(args) {
				fromScope = strings.TrimSpace(args[i+1])
				i++
			}
		}
	}
	if vendorID != "" && profileID != "" {
		ref, err := scope.NewRef(vendorID, profileID)
		if err == nil {
			return ref, true
		}
	}
	if fromScope != "" {
		if ref, err := scope.ScopeFromID(fromScope); err == nil {
			return ref, true
		}
	}
	if !activeFlag {
		return scope.Ref{}, false
	}
	paths := scope.BuildPaths(a.home, a.cwd, scope.MustRef("codex", "default"))
	active, err := control.LoadActive(paths.ActivePath)
	if err != nil || strings.TrimSpace(active.ActiveVendor) == "" || strings.TrimSpace(active.ActiveProfile) == "" {
		return scope.Ref{}, false
	}
	ref, err := scope.NewRef(active.ActiveVendor, active.ActiveProfile)
	if err != nil {
		return scope.Ref{}, false
	}
	return ref, true
}

func serviceLabelsForRuntime(rt runtime, username string) (string, string) {
	proxyLabel := strings.TrimSpace(rt.State.Service.ProxyLabel)
	syncLabel := strings.TrimSpace(rt.State.Service.SyncLabel)
	if proxyLabel != "" && syncLabel != "" {
		return proxyLabel, syncLabel
	}
	return rt.Ref.Labels(username)
}

func launchAgentPlistPaths(paths scope.Paths, proxyLabel, syncLabel string) (string, string) {
	proxy := strings.TrimSpace(proxyLabel)
	sync := strings.TrimSpace(syncLabel)
	proxyPath := paths.ProxyPlistPath
	syncPath := paths.SyncPlistPath
	if proxy != "" {
		proxyPath = filepath.Join(paths.LaunchAgentDir, proxy+".plist")
	}
	if sync != "" {
		syncPath = filepath.Join(paths.LaunchAgentDir, sync+".plist")
	}
	return proxyPath, syncPath
}

func allowSettingsMutation(active control.ActivePointer, ref scope.Ref, generation string) bool {
	// Older active pointers may not have generation tracking. In that case, still require scope identity.
	if strings.TrimSpace(active.ActiveGeneration) == "" {
		if active.ActiveVendor == "" && active.ActiveProfile == "" {
			return true
		}
		return active.ActiveVendor == ref.VendorID && active.ActiveProfile == ref.ProfileID
	}
	if active.ActiveVendor != ref.VendorID || active.ActiveProfile != ref.ProfileID {
		return false
	}
	if strings.TrimSpace(generation) == "" {
		return false
	}
	return active.ActiveGeneration == generation
}

func ensureActiveSwitchContract(active control.ActivePointer, st state.State, ref scope.Ref, command string) error {
	if active.ActiveVendor != ref.VendorID || active.ActiveProfile != ref.ProfileID {
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("%s blocked: active scope mismatch (active=%s:%s target=%s)", command, active.ActiveVendor, active.ActiveProfile, ref.ScopeID()),
		)
	}
	stateGen := strings.TrimSpace(st.Claude.AppliedGeneration)
	activeGen := strings.TrimSpace(active.ActiveGeneration)
	if st.Claude.Applied && stateGen == "" {
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("%s blocked: scope has applied claude settings but empty state generation; run: ccb claude apply --vendor %s --profile %s", command, ref.VendorID, ref.ProfileID),
		)
	}
	if st.Claude.Applied && activeGen == "" {
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("%s blocked: active generation is empty while scope is applied; run: ccb claude apply --vendor %s --profile %s", command, ref.VendorID, ref.ProfileID),
		)
	}
	if stateGen != "" && activeGen != "" && stateGen != activeGen {
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("%s blocked: active/state generation mismatch (active=%s state=%s); run: ccb claude apply --vendor %s --profile %s", command, activeGen, stateGen, ref.VendorID, ref.ProfileID),
		)
	}
	return nil
}

func reusableClaudeSnapshot(st state.State) (string, string, bool) {
	if !st.Claude.Applied {
		return "", "", false
	}
	path := strings.TrimSpace(st.Claude.SnapshotPath)
	sha := strings.TrimSpace(st.Claude.SnapshotSHA256)
	if path == "" || sha == "" {
		return "", "", false
	}
	actual, err := state.HashFile(path)
	if err != nil {
		return "", "", false
	}
	if !strings.EqualFold(actual, sha) {
		return "", "", false
	}
	return path, sha, true
}

func findCheck(checks []doctor.CheckResult, name string) (doctor.CheckResult, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctor.CheckResult{}, false
}

func snapshotSettingsBackup(settingsPath, snapshotDir, prefix string) (string, string, error) {
	settingsPath = strings.TrimSpace(settingsPath)
	if settingsPath == "" {
		return "", "", fmt.Errorf("settings path is empty")
	}
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(prefix) == "" {
		prefix = "settings-backup"
	}
	snapshotPath := filepath.Join(snapshotDir, fmt.Sprintf("%s-%d.json", prefix, time.Now().UTC().UnixNano()))

	body, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", "", err
		}
		body = []byte("{}\n")
	}
	if len(body) == 0 {
		body = []byte("{}\n")
	}
	if err := os.WriteFile(snapshotPath, body, 0o600); err != nil {
		return "", "", err
	}
	snapshotSHA := state.HashBytes(body)
	actualSHA, err := state.HashFile(snapshotPath)
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(actualSHA, snapshotSHA) {
		return "", "", fmt.Errorf("snapshot hash mismatch expected=%s actual=%s", snapshotSHA, actualSHA)
	}
	return snapshotPath, snapshotSHA, nil
}

type pathCount struct {
	Path  string
	Count int
}

type proxyLogSummary struct {
	Total       int
	Chat        int
	CountTokens int
	ByPath      map[string]int
}

func (s proxyLogSummary) TopPaths(limit int) []pathCount {
	if limit <= 0 || len(s.ByPath) == 0 {
		return nil
	}
	out := make([]pathCount, 0, len(s.ByPath))
	for path, count := range s.ByPath {
		out = append(out, pathCount{Path: path, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Path < out[j].Path
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > limit {
		return out[:limit]
	}
	return out
}

func summarizeProxyLog(path string, since time.Duration, now time.Time) (proxyLogSummary, error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	f, err := os.Open(path)
	if err != nil {
		return proxyLogSummary{}, err
	}
	defer f.Close()

	sum := proxyLogSummary{ByPath: map[string]int{}}
	cutoff := now.Add(-since)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ts, rawPath, ok := parseProxyLogTrafficLine(sc.Text())
		if !ok || ts.Before(cutoff) {
			continue
		}
		p := normalizeRequestPath(rawPath)
		sum.Total++
		sum.ByPath[p]++
		if strings.HasPrefix(p, "/v1/messages/count_tokens") {
			sum.CountTokens++
			continue
		}
		if strings.HasPrefix(p, "/v1/messages") || strings.HasPrefix(p, "/v1/chat/completions") {
			sum.Chat++
		}
	}
	if err := sc.Err(); err != nil {
		return proxyLogSummary{}, err
	}
	return sum, nil
}

func parseProxyLogTrafficLine(line string) (time.Time, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return time.Time{}, "", false
	}
	open := strings.IndexByte(line, '[')
	close := strings.IndexByte(line, ']')
	if open < 0 || close <= open+1 {
		return time.Time{}, "", false
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", line[open+1:close], time.Local)
	if err != nil {
		return time.Time{}, "", false
	}
	q1 := strings.IndexByte(line, '"')
	if q1 < 0 {
		return time.Time{}, "", false
	}
	q2rel := strings.IndexByte(line[q1+1:], '"')
	if q2rel < 0 {
		return time.Time{}, "", false
	}
	path := strings.TrimSpace(line[q1+1 : q1+1+q2rel])
	if path == "" {
		return time.Time{}, "", false
	}
	return ts, path, true
}

func normalizeRequestPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if strings.TrimSpace(u.Path) != "" {
		return u.Path
	}
	return raw
}

const suppressBootstrapOutputEnv = "CCB_SUPPRESS_BOOTSTRAP_OUTPUT"
const disableStartRecoveryEnv = "CCB_DISABLE_START_RECOVERY"

func suppressBootstrapOutput() bool {
	v := strings.TrimSpace(os.Getenv(suppressBootstrapOutputEnv))
	if v == "" {
		return false
	}
	b, err := parseBool(v)
	if err != nil {
		// Non-empty toggle values are treated as enabled.
		return true
	}
	return b
}

func disableStartRecovery() bool {
	v := strings.TrimSpace(os.Getenv(disableStartRecoveryEnv))
	if v == "" {
		return false
	}
	b, err := parseBool(v)
	if err != nil {
		return true
	}
	return b
}

func withEnvOverride(key, value string, fn func() error) error {
	prev, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		return err
	}
	defer func() {
		if had {
			_ = os.Setenv(key, prev)
			return
		}
		_ = os.Unsetenv(key)
	}()
	return fn()
}

type expectedActiveRequirement struct {
	VendorID   string
	ProfileID  string
	Generation string
}

func ensureExpectedActive(active control.ActivePointer, expected expectedActiveRequirement, command string) error {
	if active.ActiveVendor != expected.VendorID || active.ActiveProfile != expected.ProfileID {
		activeScope := "<unset>"
		if strings.TrimSpace(active.ActiveVendor) != "" && strings.TrimSpace(active.ActiveProfile) != "" {
			activeScope = fmt.Sprintf("%s:%s", active.ActiveVendor, active.ActiveProfile)
		}
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("%s blocked: expected active=%s:%s but got active=%s", command, expected.VendorID, expected.ProfileID, activeScope),
		)
	}
	if expected.Generation != "" && strings.TrimSpace(active.ActiveGeneration) != expected.Generation {
		return cberr.New(
			cberr.ErrSwitchValidation,
			fmt.Sprintf("%s blocked: expected active generation=%s but got=%s", command, expected.Generation, strings.TrimSpace(active.ActiveGeneration)),
		)
	}
	return nil
}

func isRecoverableSetupServiceStartError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "could not find service") {
		return true
	}
	if strings.Contains(msg, "connection refused") {
		return true
	}
	return false
}

func capabilityImplemented(bundle provider.Bundle, cap provider.Capability) bool {
	switch cap {
	case provider.CapabilityAuth:
		return bundle.Auth != nil
	case provider.CapabilityClaude:
		return bundle.Claude != nil
	default:
		return false
	}
}

func backendCapabilityImplemented(bundle backend.Bundle, cap backend.Capability) bool {
	switch cap {
	case backend.CapabilityArtifact:
		return bundle.Artifact != nil
	case backend.CapabilityProxy:
		return bundle.Proxy != nil
	case backend.CapabilityHealth:
		return bundle.Health != nil
	default:
		return false
	}
}

func waitForBackendHealth(rt runtime, timeout, interval time.Duration) error {
	if timeout <= 0 {
		timeout = 1 * time.Second
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := rt.Backend.Health.Check(context.Background(), toBackendRuntime(rt)); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(interval)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("healthcheck failed")
}

type targetScopeRestorer struct {
	app                    *application
	toRef                  scope.Ref
	toPaths                scope.Paths
	toScopeExisted         bool
	prevToCfg              config.Config
	prevToState            state.State
	rollbackTargetRT       *runtime
	targetBootstrapApplied bool
}

func (r *targetScopeRestorer) restore() error {
	var restoreErrs []error
	if r.rollbackTargetRT != nil && isGatewayProxyMode(*r.rollbackTargetRT) {
		mgr := launchd.NewManager()
		proxyLabel, syncLabel := serviceLabelsForRuntime(*r.rollbackTargetRT, r.app.username)
		proxyPlistPath, syncPlistPath := launchAgentPlistPaths(r.rollbackTargetRT.Paths, proxyLabel, syncLabel)
		if err := mgr.RemoveAgents(proxyLabel, syncLabel, proxyPlistPath, syncPlistPath); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("failed to cleanup target launch agents: %w", err))
		}
	}
	if !r.toScopeExisted {
		if err := os.RemoveAll(r.toPaths.ScopeDir); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("failed to remove new target scope: %w", err))
		}
		if len(restoreErrs) > 0 {
			return stderrors.Join(restoreErrs...)
		}
		return nil
	}
	if err := config.Save(r.toPaths.ConfigPath, r.prevToCfg); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("failed to restore target config: %w", err))
	}
	if err := state.Save(r.toPaths.StatePath, r.prevToState); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("failed to restore target state: %w", err))
	}
	if r.targetBootstrapApplied && r.prevToCfg.RuntimeMode == config.RuntimeModeGateway && r.prevToCfg.ProxyEnabled {
		if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
			return r.app.cmdService([]string{"install", "--vendor", r.toRef.VendorID, "--profile", r.toRef.ProfileID})
		}); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("failed to restore target gateway service install: %w", err))
		} else if r.prevToState.Service.Running {
			if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
				return r.app.cmdService([]string{"start", "--vendor", r.toRef.VendorID, "--profile", r.toRef.ProfileID})
			}); err != nil {
				restoreErrs = append(restoreErrs, fmt.Errorf("failed to restore target gateway service start: %w", err))
			}
		} else {
			if err := withEnvOverride(suppressBootstrapOutputEnv, "1", func() error {
				return r.app.cmdService([]string{"stop", "--vendor", r.toRef.VendorID, "--profile", r.toRef.ProfileID})
			}); err != nil {
				restoreErrs = append(restoreErrs, fmt.Errorf("failed to restore target gateway service stop: %w", err))
			}
		}
	}
	if len(restoreErrs) > 0 {
		return stderrors.Join(restoreErrs...)
	}
	return nil
}

func isGatewayProxyMode(rt runtime) bool {
	return rt.Config.RuntimeMode == config.RuntimeModeGateway && rt.Config.ProxyEnabled
}

func requireGatewayProxyMode(rt runtime, command string) error {
	if isGatewayProxyMode(rt) {
		return nil
	}
	return cberr.New(
		cberr.ErrCapabilityMissing,
		fmt.Sprintf("%s is only available in runtime_mode=gateway with proxy_enabled=true", command),
	)
}

func ensureScopedSettingsBinding(rt runtime, command string) error {
	switch rt.Config.SettingsLayer {
	case config.SettingsLayerProject:
		expected := filepath.Clean(rt.Paths.ClaudeProjectSettingsPath)
		configured := filepath.Clean(strings.TrimSpace(rt.Config.SettingsPath))
		if configured == "" || configured != expected {
			return cberr.New(
				cberr.ErrConfigOverridden,
				fmt.Sprintf("%s blocked: settings_layer=project mismatch (configured=%s expected=%s for cwd=%s); run: ccb setup --vendor %s --profile %s --settings-layer project", command, rt.Config.SettingsPath, expected, rt.Paths.Cwd, rt.Ref.VendorID, rt.Ref.ProfileID),
			)
		}
	case config.SettingsLayerLocal:
		expected := filepath.Clean(rt.Paths.ClaudeLocalSettingsPath)
		configured := filepath.Clean(strings.TrimSpace(rt.Config.SettingsPath))
		if configured == "" || configured != expected {
			return cberr.New(
				cberr.ErrConfigOverridden,
				fmt.Sprintf("%s blocked: settings_layer=local mismatch (configured=%s expected=%s for cwd=%s); run: ccb setup --vendor %s --profile %s --settings-layer local", command, rt.Config.SettingsPath, expected, rt.Paths.Cwd, rt.Ref.VendorID, rt.Ref.ProfileID),
			)
		}
	}
	return nil
}

func isHelpArg(v string) bool {
	switch strings.TrimSpace(v) {
	case "-h", "--help", "-help", "help":
		return true
	default:
		return false
	}
}

func isServiceSubcommand(v string) bool {
	switch strings.TrimSpace(v) {
	case "install", "start", "reconcile", "stop", "status":
		return true
	default:
		return false
	}
}

func (a *application) runServiceReconcile(ref scope.Ref, suppressOutput bool) error {
	run := func() error {
		if err := a.cmdService([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
			return cberr.Wrap(cberr.ErrRollbackFailed, "service reconcile failed at install", err)
		}
		if err := withEnvOverride(disableStartRecoveryEnv, "1", func() error {
			return a.cmdService([]string{"start", "--vendor", ref.VendorID, "--profile", ref.ProfileID})
		}); err != nil {
			return cberr.Wrap(cberr.ErrRollbackFailed, "service reconcile failed at start", err)
		}
		return nil
	}
	if suppressOutput {
		return withEnvOverride(suppressBootstrapOutputEnv, "1", run)
	}
	return run()
}

func homeDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CCB_HOME")); v != "" {
		return v, nil
	}
	return os.UserHomeDir()
}

func currentUsername() string {
	if v := strings.TrimSpace(os.Getenv("USER")); v != "" {
		return v
	}
	return fmt.Sprintf("uid%d", os.Getuid())
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "yes", "y":
		return true, nil
	case "0", "f", "false", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean: %q", v)
	}
}

func nonEmptyOrDefault(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return fallback
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", dst, os.Getpid(), time.Now().UTC().UnixNano())
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(dst, mode)
}
