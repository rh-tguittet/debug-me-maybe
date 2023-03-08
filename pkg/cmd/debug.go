package cmd

import (
	"context"
	"debug-me-maybe/kube"
	"debug-me-maybe/pkg/config"
	"debug-me-maybe/pkg/service/debugger"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	_ "k8s.io/client-go/plugin/pkg/client/auth/azure"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

var (
	dmmExample = "kubectl dmm pod hello-minikube-7c77b68cff-qbvsd"
)

const minimumNumberOfArguments = 1
const dlvBinaryName = "dlv"
const dlvRemotePath = "/tmp/dlv"

var dlvLocalBinaryPathLookupList []string

type DMM struct {
	configFlags      *genericclioptions.ConfigFlags
	resultingContext *api.Context
	clientset        *kubernetes.Clientset
	restConfig       *rest.Config
	rawConfig        api.Config
	settings         *config.DMMSettings
	debuggerService  debugger.DebuggerService
}

func NewDMM(settings *config.DMMSettings) *DMM {
	return &DMM{settings: settings, configFlags: genericclioptions.NewConfigFlags(true)}
}

func NewCmdSniff(streams genericclioptions.IOStreams) *cobra.Command {
	dmmSettings := config.NewDMMSettings(streams)

	dmm := NewDMM(dmmSettings)

	cmd := &cobra.Command{
		Use:          "dmm pod [-n namespace] [-c container] [-P pid]",
		Short:        "Debug Me Maybe. Attaches a dlv debugger on a running process in a pod.",
		Example:      dmmExample,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if err := dmm.Complete(c, args); err != nil {
				return err
			}
			if err := dmm.Validate(); err != nil {
				return err
			}
			if err := dmm.Run(); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&dmmSettings.UserSpecifiedNamespace, "namespace", "n", "", "namespace (optional)")
	_ = viper.BindEnv("namespace", "KUBECTL_PLUGINS_CURRENT_NAMESPACE")
	_ = viper.BindPFlag("namespace", cmd.Flags().Lookup("namespace"))

	cmd.Flags().IntVarP(&dmmSettings.UserSpecifiedPid, "pid", "P", 1, "PID of the process to debug (optional)")
	_ = viper.BindEnv("pid", "KUBECTL_PLUGINS_LOCAL_FLAG_PID")
	_ = viper.BindPFlag("pid", cmd.Flags().Lookup("pid"))

	cmd.Flags().StringVarP(&dmmSettings.UserSpecifiedContainer, "container", "c", "", "container (optional)")
	_ = viper.BindEnv("container", "KUBECTL_PLUGINS_LOCAL_FLAG_CONTAINER")
	_ = viper.BindPFlag("container", cmd.Flags().Lookup("container"))

	cmd.Flags().BoolVarP(&dmmSettings.UserSpecifiedVerboseMode, "verbose", "v", false,
		"if specified, dmm output will include debug information (optional)")
	_ = viper.BindEnv("verbose", "KUBECTL_PLUGINS_LOCAL_FLAG_VERBOSE")
	_ = viper.BindPFlag("verbose", cmd.Flags().Lookup("verbose"))

	cmd.Flags().StringVarP(&dmmSettings.UserSpecifiedKubeContext, "context", "x", "",
		"kubectl context to work on (optional)")
	_ = viper.BindEnv("context", "KUBECTL_PLUGINS_CURRENT_CONTEXT")
	_ = viper.BindPFlag("context", cmd.Flags().Lookup("context"))

	cmd.Flags().StringVarP(&dmmSettings.UserSpecifiedLocalDlvPath, "local-dlv-path", "f", "",
		"local dlv binary path (optional)")
	_ = viper.BindEnv("local-dlv-path", "KUBECTL_PLUGINS_LOCAL_FLAG_LOCAL_DLV_PATH")
	_ = viper.BindPFlag("local-dlv-path", cmd.Flags().Lookup("local-dlv-path"))

	cmd.Flags().StringVarP(&dmmSettings.UserSpecifiedRemoteDlvPath, "remote-dlv-path", "r", dlvRemotePath,
		"remote dlv binary path (optional)")
	_ = viper.BindEnv("remote-dlv-path", "KUBECTL_PLUGINS_LOCAL_FLAG_REMOTE_DLV_PATH")
	_ = viper.BindPFlag("remote-dlv-path", cmd.Flags().Lookup("remote-dlv-path"))

	cmd.Flags().IntVarP(&dmmSettings.UserSpecifiedDebuggerPort, "debugger-port", "d", 2345,
		"remote dlv port to listen on (optional)")
	_ = viper.BindEnv("debugger-port", "KUBECTL_PLUGINS_LOCAL_FLAG_DEBUGGER_PORT")
	_ = viper.BindPFlag("debugger-port", cmd.Flags().Lookup("debugger-port"))

	cmd.Flags().BoolVarP(&dmmSettings.UserSpecifiedForceKill, "force-kill", "k", false,
		"if specified, dmm will attempt to kill a remote dlv process and quit (optional)")
	_ = viper.BindEnv("force-kill", "KUBECTL_PLUGINS_LOCAL_FLAG_FORCE_KILL")
	_ = viper.BindPFlag("force-kill", cmd.Flags().Lookup("force-kill"))

	return cmd
}

func (o *DMM) Complete(cmd *cobra.Command, args []string) error {

	if len(args) < minimumNumberOfArguments {
		_ = cmd.Usage()
		return errors.New("not enough arguments, pod name missing")
	}

	o.settings.UserSpecifiedPodName = args[0]
	if o.settings.UserSpecifiedPodName == "" {
		return errors.New("pod name is empty")
	}

	o.settings.UserSpecifiedNamespace = viper.GetString("namespace")
	o.settings.UserSpecifiedContainer = viper.GetString("container")
	o.settings.UserSpecifiedPid = viper.GetInt("pid")
	o.settings.UserSpecifiedVerboseMode = viper.GetBool("verbose")
	o.settings.UserSpecifiedKubeContext = viper.GetString("context")
	o.settings.UserSpecifiedLocalDlvPath = viper.GetString("local-dlv-path")
	o.settings.UserSpecifiedRemoteDlvPath = viper.GetString("remote-dlv-path")
	o.settings.UserSpecifiedDebuggerPort = viper.GetInt("debugger-port")
	o.settings.UserSpecifiedForceKill = viper.GetBool("force-kill")

	var err error

	if o.settings.UserSpecifiedVerboseMode {
		log.Info("running in verbose mode")
		log.SetLevel(log.DebugLevel)
	}

	dlvLocalBinaryPathLookupList, err = o.buildDlvBinaryPathLookupList()
	if err != nil {
		return err
	}

	o.rawConfig, err = o.configFlags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return err
	}

	var currentContext *api.Context
	var exists bool

	if o.settings.UserSpecifiedKubeContext != "" {
		currentContext, exists = o.rawConfig.Contexts[o.settings.UserSpecifiedKubeContext]
	} else {
		currentContext, exists = o.rawConfig.Contexts[o.rawConfig.CurrentContext]
	}

	if !exists {
		return errors.New("context doesn't exist")
	}

	o.restConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: o.configFlags.ToRawKubeConfigLoader().ConfigAccess().GetDefaultFilename()},
		&clientcmd.ConfigOverrides{
			CurrentContext: o.settings.UserSpecifiedKubeContext,
		}).ClientConfig()

	if err != nil {
		return err
	}

	o.restConfig.Timeout = 30 * time.Second

	o.clientset, err = kubernetes.NewForConfig(o.restConfig)
	if err != nil {
		return err
	}

	o.resultingContext = currentContext.DeepCopy()
	if o.settings.UserSpecifiedNamespace != "" {
		o.resultingContext.Namespace = o.settings.UserSpecifiedNamespace
	}

	return nil
}

func (o *DMM) buildDlvBinaryPathLookupList() ([]string, error) {
	dlvBinaryPath, err := filepath.EvalSymlinks(os.Args[0])
	if err != nil {
		return nil, err
	}

	dlvBinaryPath = filepath.Join(filepath.Dir(dlvBinaryPath), dlvBinaryName)

	return append([]string{o.settings.UserSpecifiedLocalDlvPath, dlvBinaryPath}), nil
}

func (o *DMM) Validate() error {
	if len(o.rawConfig.CurrentContext) == 0 {
		return errors.New("context doesn't exist")
	}

	if o.resultingContext.Namespace == "" {
		return errors.New("namespace value is empty should be custom or default")
	}

	var err error

	o.settings.UserSpecifiedLocalDlvPath, err = findLocalDlvBinaryPath()
	if err != nil {
		return err
	}

	log.Infof("using dlv path at: '%s'", o.settings.UserSpecifiedLocalDlvPath)

	pod, err := o.clientset.CoreV1().Pods(o.resultingContext.Namespace).Get(context.TODO(), o.settings.UserSpecifiedPodName, v1.GetOptions{})
	if err != nil {
		return err
	}

	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return errors.Errorf("cannot debug a pid in a container in a completed pod; current phase is %s", pod.Status.Phase)
	}

	o.settings.DetectedPodNodeName = pod.Spec.NodeName

	log.Debugf("pod '%s' status: '%s'", o.settings.UserSpecifiedPodName, pod.Status.Phase)

	if len(pod.Spec.Containers) < 1 {
		return errors.New("no containers in specified pod")
	}

	if o.settings.UserSpecifiedContainer == "" {
		log.Info("no container specified, taking first container we found in pod.")
		o.settings.UserSpecifiedContainer = pod.Spec.Containers[0].Name
		log.Infof("selected container: '%s'", o.settings.UserSpecifiedContainer)
	}

	if err := o.findContainerId(pod); err != nil {
		return err
	}

	kubernetesApiService := kube.NewKubernetesApiService(o.clientset, o.restConfig, o.resultingContext.Namespace)

	if o.settings.UserSpecifiedDebuggerPort < 1024 || o.settings.UserSpecifiedDebuggerPort > 65535 {
		return errors.New("Debugger port must be between 1024 and 65535")
	}

	log.Info("debugging method: upload static dlv")
	o.debuggerService = debugger.NewUploadDlvRemoteDebuggingService(o.settings, kubernetesApiService)

	return nil
}

func (o *DMM) findContainerId(pod *corev1.Pod) error {
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if o.settings.UserSpecifiedContainer == containerStatus.Name {
			result := strings.Split(containerStatus.ContainerID, "://")
			if len(result) != 2 {
				break
			}
			o.settings.DetectedContainerRuntime = result[0]
			o.settings.DetectedContainerId = result[1]
			return nil
		}
	}

	return errors.Errorf("couldn't find container: '%s' in pod: '%s'", o.settings.UserSpecifiedContainer, o.settings.UserSpecifiedPodName)
}

func findLocalDlvBinaryPath() (string, error) {
	log.Debugf("searching for dlv binary using lookup list: '%v'", dlvLocalBinaryPathLookupList)

	for _, possibleDlvPath := range dlvLocalBinaryPathLookupList {
		if _, err := os.Stat(possibleDlvPath); err == nil {
			log.Debugf("dlv binary found at: '%s'", possibleDlvPath)

			return possibleDlvPath, nil
		}

		log.Debugf("dlv binary was not found at: '%s'", possibleDlvPath)
	}

	return "", errors.Errorf("couldn't find dlv binary on any of: '%v'", dlvLocalBinaryPathLookupList)
}

func (o *DMM) Run() error {
	log.Infof("debugging on pod: '%s' [namespace: '%s', container: '%s', pid: '%d', port: '%d']",
		o.settings.UserSpecifiedPodName, o.resultingContext.Namespace, o.settings.UserSpecifiedContainer, o.settings.UserSpecifiedPid, o.settings.UserSpecifiedDebuggerPort)

	err := o.debuggerService.Setup()
	if err != nil {
		return err
	}

	cleanupFunc := func() {
		log.Info("starting debugger cleanup")

		err := o.debuggerService.Cleanup()
		if err != nil {
			log.WithError(err).Error("failed to teardown debugger, a manual teardown is required.")
			return
		}

		log.Info("debugger cleanup completed successfully")
	}

	if o.settings.UserSpecifiedForceKill {
		log.Infof("Attempting to kill a remote dlv debugger by its path '%s'", o.settings.UserSpecifiedRemoteDlvPath)
		cleanupFunc()
		return nil
	} else {
		defer cleanupFunc()
	}

	log.Infof("starting kubectl port-forward on port %d", o.settings.UserSpecifiedDebuggerPort)

	// Resolves the kubeconfig file location for the port-forward
	kubectlContext := "~/.kube/config"

	if o.settings.UserSpecifiedKubeContext != "" {
		kubectlContext = o.settings.UserSpecifiedKubeContext
	} else if c, ok := os.LookupEnv("KUBECONFIG"); ok {
		kubectlContext = c
	}

	// Using kubectl here as to not have to rewrite the logic
	cmd := exec.Command("kubectl",
		"-n", o.settings.UserSpecifiedNamespace,
		"--kubeconfig", kubectlContext, /* if we don't set this, it might default to some kubeconfig we don't expect */
		"port-forward",
		fmt.Sprintf("pod/%s", o.settings.UserSpecifiedPodName),
		fmt.Sprintf("%d:%d", o.settings.UserSpecifiedDebuggerPort, o.settings.UserSpecifiedDebuggerPort))

	l := log.WithFields(log.Fields{
		"remote": log.Fields{
			"namespace": o.settings.UserSpecifiedNamespace,
			"pod":       o.settings.UserSpecifiedPodName,
			"container": o.settings.UserSpecifiedContainer,
			"pid":       o.settings.UserSpecifiedPid,
		},
		"port-forward": o.settings.UserSpecifiedDebuggerPort,
	})

	cmd.Stdout = l.Writer()
	cmd.Stderr = l.Writer()

	if err != nil {
		return err
	}

	go func() {
		l := log.WithFields(log.Fields{
			"namespace": o.settings.UserSpecifiedNamespace,
			"pod":       o.settings.UserSpecifiedPodName,
			"container": o.settings.UserSpecifiedContainer,
			"pid":       o.settings.UserSpecifiedPid,
			"dlv":       o.settings.UserSpecifiedDebuggerPort,
		})
		err := o.debuggerService.Start(l.Writer())
		if err != nil {
			log.WithError(err).Errorf("failed to start remote debugging, stopping port-forward")
			_ = cmd.Process.Kill()
		}
	}()

	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}
