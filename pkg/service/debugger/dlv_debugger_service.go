package debugger

import (
	"debug-me-maybe/kube"
	"debug-me-maybe/pkg/config"
	"fmt"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"io"
	"strconv"
	"strings"
)

type DlvDebuggerService struct {
	settings             *config.DMMSettings
	kubernetesApiService kube.KubernetesApiService
}

func NewUploadDlvRemoteDebuggingService(options *config.DMMSettings, service kube.KubernetesApiService) DebuggerService {
	return &DlvDebuggerService{settings: options, kubernetesApiService: service}
}

func (u *DlvDebuggerService) Setup() error {
	log.Infof("uploading dlv binary from: '%s' to: '%s'",
		u.settings.UserSpecifiedLocalDlvPath, u.settings.UserSpecifiedRemoteDlvPath)

	err := u.kubernetesApiService.UploadFile(u.settings.UserSpecifiedLocalDlvPath,
		u.settings.UserSpecifiedRemoteDlvPath, u.settings.UserSpecifiedPodName, u.settings.UserSpecifiedContainer)

	if err != nil {
		log.WithError(err).Errorf("failed uploading dlv binary to container, please verify the remote container has tar installed")
		return err
	}

	log.Info("dlv uploaded successfully")

	return nil
}

func (u *DlvDebuggerService) Cleanup() error {
	log.Info("killing dlv process on remote container")

	commandPidof := []string{
		"pidof",
		u.settings.UserSpecifiedRemoteDlvPath,
	}

	pidofOutput := new(kube.Writer)
	exitCode, err := u.kubernetesApiService.ExecuteCommand(u.settings.UserSpecifiedPodName, u.settings.UserSpecifiedContainer, commandPidof, pidofOutput)

	if err != nil || exitCode != 0 {
		return errors.Errorf("failed to execute 'pidof %s' with exit code: '%d', perhaps the debugger is already closed?", u.settings.UserSpecifiedRemoteDlvPath, exitCode)
	}

	dlvPid, err := strconv.ParseInt(strings.TrimSpace(pidofOutput.Output), 10, 64)

	if err != nil {
		return errors.Errorf("failed to convert the retrieved pid of dlv to an integer: %s", err)
	}

	commandKill := []string{
		"kill",
		"-15",
		strconv.Itoa(int(dlvPid)),
	}

	exitCode, err = u.kubernetesApiService.ExecuteCommand(u.settings.UserSpecifiedPodName, u.settings.UserSpecifiedContainer, commandKill, nil)

	if err != nil || exitCode != 0 {
		return errors.Errorf("failed to kill dlv pid '%d' with exit code: '%d'", dlvPid, exitCode)
	}

	log.Infof("remote dlv process killed")

	return nil
}

func (u *DlvDebuggerService) Start(stdOut io.Writer) error {
	log.Info("start debugging on remote container")

	command := []string{
		u.settings.UserSpecifiedRemoteDlvPath,
		"attach",
		strconv.Itoa(u.settings.UserSpecifiedPid),
		"--continue",
		"--accept-multiclient",
		"--log",
		fmt.Sprintf("--listen=:%d", u.settings.UserSpecifiedDebuggerPort),
		"--headless=true",
		"--api-version=2",
	}

	exitCode, err := u.kubernetesApiService.ExecuteCommand(u.settings.UserSpecifiedPodName, u.settings.UserSpecifiedContainer, command, stdOut)
	if err != nil || exitCode != 0 {
		return errors.Errorf("executing debugger failed, exit code: '%d'", exitCode)
	}

	log.Infof("debugging on remote container")

	return nil
}
