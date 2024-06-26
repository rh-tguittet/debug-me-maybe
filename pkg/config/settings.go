package config

import (
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

type UploadMethod string

const (
	DIRECT UploadMethod = "direct"
	STAGER UploadMethod = "stager"
)

type DMMSettings struct {
	UserSpecifiedPodName       string
	UserSpecifiedContainer     string
	UserSpecifiedNamespace     string
	UserSpecifiedVerboseMode   bool
	UserSpecifiedImage         string
	UserSpecifiedPid           int
	DetectedPodNodeName        string
	DetectedContainerId        string
	DetectedContainerRuntime   string
	UserSpecifiedKubeContext   string
	UserSpecifiedLocalDlvPath  string
	UserSpecifiedRemoteDlvPath string
	UserSpecifiedDebuggerPort  int
	UserSpecifiedForceKill     bool
	UserSpecifiedUploadMethod  UploadMethod
}

func NewDMMSettings(streams genericclioptions.IOStreams) *DMMSettings {
	return &DMMSettings{}
}
