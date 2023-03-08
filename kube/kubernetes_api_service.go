package kube

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"io"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type KubernetesApiService interface {
	ExecuteCommand(podName string, containerName string, command []string, stdOut io.Writer) (int, error)

	DeletePod(podName string) error

	UploadFile(localPath string, remotePath string, podName string, containerName string) error
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

	err := k.clientset.CoreV1().Pods(k.targetNamespace).Delete(context.TODO(), podName, v1.DeleteOptions{
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

func (k *KubernetesApiServiceImpl) UploadFile(localPath string, remotePath string, podName string, containerName string) error {
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
