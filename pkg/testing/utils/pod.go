// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
)

// WaitTimeoutForPodsRunning waits the given timeout duration for the specified pods to become running.
func WaitTimeoutForPodsRunning(c clientset.Interface, pods []corev1.Pod, timeout time.Duration) error {
	return wait.PollImmediate(time.Second*2, timeout, podsRunning(c, pods))
}

func WaitTimeoutForPodScheduled(c clientset.Interface, pod corev1.Pod, timeout time.Duration) error {
	return wait.PollImmediate(time.Second*2, timeout, podScheduled(c, pod))
}

func podScheduled(c clientset.Interface, pod corev1.Pod) wait.ConditionFunc {
	return func() (bool, error) {
		curPod, err := c.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if curPod.Spec.NodeName == "" {
			return false, nil
		}
		return true, nil
	}
}

func podsRunning(c clientset.Interface, pods []corev1.Pod) wait.ConditionFunc {
	return func() (bool, error) {
		for _, pod := range pods {
			curPod, err := c.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if curPod.Status.Phase != corev1.PodRunning {
				return false, nil
			}
		}
		return true, nil
	}
}
