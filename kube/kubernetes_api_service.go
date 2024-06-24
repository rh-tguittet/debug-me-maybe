package kube

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"io"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/pointer"
	"time"
)

type KubernetesApiService interface {
	ExecuteCommand(podName string, containerName string, command []string, stdOut io.Writer) (int, error)

	DeletePod(podName string) error

	UploadFileTar(localPath string, remotePath string, podName string, containerName string) error
	UploadThroughCurl(localPath string, remotePath string, podName string, containerName string) error
}

type KubernetesApiServiceImpl struct {
	clientset       *kubernetes.Clientset
	restConfig      *rest.Config
	targetNamespace string
}

func NewKubernetesApiService(clientset *kubernetes.Clientset,
	restConfig *rest.Config, targetNamespace string) KubernetesApiService {

	return &KubernetesApiServiceImpl{clientset: clientset,
		restConfig:      restConfig,
		targetNamespace: targetNamespace}
}

func (k *KubernetesApiServiceImpl) ExecuteCommand(podName string, containerName string, command []string, stdOut io.Writer) (int, error) {

	log.Infof("executing command: '%s' on container: '%s', pod: '%s', namespace: '%s'", command, containerName, podName, k.targetNamespace)
	stdErr := new(Writer)

	executeDlvRequest := ExecCommandRequest{
		KubeRequest: KubeRequest{
			Clientset:  k.clientset,
			RestConfig: k.restConfig,
			Namespace:  k.targetNamespace,
			Pod:        podName,
			Container:  containerName,
		},
		Command: command,
		StdErr:  stdErr,
		StdOut:  stdOut,
	}

	exitCode, err := PodExecuteCommand(executeDlvRequest)
	if err != nil {
		log.WithError(err).Errorf("failed executing command: '%s', exitCode: '%d', stdErr: '%s'",
			command, exitCode, stdErr.Output)

		return exitCode, err
	}

	log.Infof("command: '%s' executing successfully exitCode: '%d', stdErr :'%s'", command, exitCode, stdErr.Output)

	return exitCode, err
}

func (k *KubernetesApiServiceImpl) DeletePod(podName string) error {

	log.Infof("removing privileged pod: '%s'", podName)
	defer log.Infof("privileged pod: '%s' removed", podName)

	var gracePeriodTime int64 = 0

	err := k.clientset.CoreV1().Pods(k.targetNamespace).Delete(context.Background(), podName, v1.DeleteOptions{
		GracePeriodSeconds: &gracePeriodTime,
	})

	return err
}

func (k *KubernetesApiServiceImpl) checkIfFileExistOnPod(remotePath string, podName string, containerName string) (bool, error) {
	stdOut := new(Writer)
	stdErr := new(Writer)

	command := []string{"/bin/sh", "-c", fmt.Sprintf("test -f %s", remotePath)}

	exitCode, err := k.ExecuteCommand(podName, containerName, command, stdOut)
	if err != nil {
		return false, err
	}

	if exitCode != 0 {
		return false, nil
	}

	if stdErr.Output != "" {
		return false, errors.New("failed to check for dlv")
	}

	log.Infof("file found: '%s'", stdOut.Output)

	return true, nil
}

func (k *KubernetesApiServiceImpl) UploadThroughCurl(localPath string, remotePath string, podName string, containerName string) error {
	log.Infof("Checking if file exists on the pod: '%s'", remotePath)
	isExist, err := k.checkIfFileExistOnPod(remotePath, podName, containerName)
	if err != nil {
		return err
	}

	if isExist {
		log.Info("file was already found on remote pod")
		return nil
	}

	// 1. Launch a python pod w/ service (http server)
	log.Infof("Create file serving pod")
	pod, err := k.clientset.CoreV1().Pods(k.targetNamespace).Create(context.Background(), &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "dmm-stager-",
			Namespace:    k.targetNamespace,
			Labels: map[string]string{
				"app": "dmm-stager",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "stager",
					Image: "docker.io/library/python:latest",
					Command: []string{
						"python3",
						"-m",
						"http.server",
					},
					WorkingDir: "/tmp",
					Ports: []corev1.ContainerPort{
						{
							Protocol:      "TCP",
							ContainerPort: 8000,
						},
					},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: pointer.Bool(false),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{
								"All",
							},
						},
						RunAsNonRoot: pointer.Bool(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: "RuntimeDefault",
						},
					},
				},
			},
		},
	}, v1.CreateOptions{})
	if err != nil {
		log.WithError(err).Errorf("failed to create pod")
		return err
	}

	defer func() {
		err := k.clientset.CoreV1().Pods(k.targetNamespace).Delete(context.Background(), pod.Name, v1.DeleteOptions{})
		if err != nil {
			log.WithError(err).Errorf("failed to delete stager pod")
		} else {
			log.Infof("stager deleted pod")
		}
	}()

	log.Infof("Waiting for staging pod to become ready")

	i := 0

	for ; i < 10; i++ {
		time.Sleep(time.Second)
		stagerPod, err := k.clientset.CoreV1().Pods(k.targetNamespace).Get(context.Background(), pod.Name, v1.GetOptions{})
		if err != nil {
			return err
		}

		if stagerPod.Status.Phase == corev1.PodRunning {
			log.Infof("Stager pod is running")
			i = 0
			break
		}
	}

	if i == 10 {
		return errors.New("stager pod is not running, something went wrong, exiting")
	}

	log.Infof("Creating service for staging pod")
	svc, err := k.clientset.CoreV1().Services(k.targetNamespace).Create(context.Background(), &corev1.Service{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "dmm-stager-",
			Namespace:    k.targetNamespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Protocol: "TCP",
					Port:     8000,
				},
			},
			Selector: map[string]string{
				"app": "dmm-stager",
			},
		},
	}, v1.CreateOptions{})

	defer func() {
		err := k.clientset.CoreV1().Services(k.targetNamespace).Delete(context.Background(), svc.Name, v1.DeleteOptions{})
		if err != nil {
			log.WithError(err).Errorf("failed to delete stager service")
		} else {
			log.Infof("stager deleted service")
		}
	}()

	stagingDebuggerUrl := fmt.Sprintf("http://%s.%s.svc.cluster.local:8000/debugger", svc.Name, k.targetNamespace)
	log.Infof("The staged debugger is available at: %s", stagingDebuggerUrl)

	// 2. Copy the debugger to the pod
	log.Infof("Uploading debugger to staging pod")
	err = k.UploadFileTar(localPath, "debugger", pod.Name, "stager")
	if err != nil {
		log.WithError(err).Errorf("failed to upload debugger to stager pod")
		return err
	}

	// 3. Curl the debugger onto the pod to debug
	log.Infof("Retrieving the debugger from the staging pod")
	stdErr := new(Writer)
	exitCode, err := PodExecuteCommand(ExecCommandRequest{
		KubeRequest: KubeRequest{
			Clientset:  k.clientset,
			RestConfig: k.restConfig,
			Namespace:  k.targetNamespace,
			Pod:        podName,
			Container:  containerName,
		},
		Command: []string{
			"curl",
			"-o", remotePath,
			stagingDebuggerUrl,
		},
		StdIn:  nil,
		StdOut: stdErr,
		StdErr: stdErr,
	})
	if err != nil {
		log.WithError(err).Errorf("failed to curl the staged debugger: exitCode: '%d', stdOut/stdErr: '%s'", exitCode, stdErr.Output)
		return err
	}

	// 4. Set the debugger as executable
	log.Infof("Setting the debugger as executable")
	exitCodeBis, err := PodExecuteCommand(ExecCommandRequest{
		KubeRequest: KubeRequest{
			Clientset:  k.clientset,
			RestConfig: k.restConfig,
			Namespace:  k.targetNamespace,
			Pod:        podName,
			Container:  containerName,
		},
		Command: []string{
			"chmod",
			"+x", remotePath,
		},
		StdIn:  nil,
		StdOut: stdErr,
		StdErr: stdErr,
	})
	if err != nil {
		log.WithError(err).Errorf("Failed to mark the debugger as executable: exitCode: '%d', stdOut/stdErr: '%s'", exitCodeBis, stdErr.Output)
		return err
	}

	log.Infof("Debugger uploaded on the debugger pod")

	return nil
}

func (k *KubernetesApiServiceImpl) UploadFileTar(localPath string, remotePath string, podName string, containerName string) error {
	log.Infof("uploading file: '%s' to '%s' on container: '%s'", localPath, remotePath, containerName)

	isExist, err := k.checkIfFileExistOnPod(remotePath, podName, containerName)
	if err != nil {
		return err
	}

	if isExist {
		log.Info("file was already found on remote pod")
		return nil
	}

	log.Infof("file not found at: '%s', starting to upload", remotePath)

	req := UploadFileRequest{
		KubeRequest: KubeRequest{
			Clientset:  k.clientset,
			RestConfig: k.restConfig,
			Namespace:  k.targetNamespace,
			Pod:        podName,
			Container:  containerName,
		},
		Src: localPath,
		Dst: remotePath,
	}

	exitCode, err := PodUploadFile(req)
	if err != nil || exitCode != 0 {
		return errors.Wrapf(err, "upload file failed, exitCode: %d", exitCode)
	}

	log.Info("verifying file uploaded successfully")

	isExist, err = k.checkIfFileExistOnPod(remotePath, podName, containerName)
	if err != nil {
		return err
	}

	if !isExist {
		log.Error("failed to upload file.")
		return errors.New("couldn't locate file on pod after upload done")
	}

	log.Info("file uploaded successfully")

	return nil
}
