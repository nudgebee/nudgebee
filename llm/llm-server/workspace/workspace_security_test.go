package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyWorkspaceSecurityDefaults(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "workspace-init"}},
			Containers:     []corev1.Container{{Name: "workspace-server"}},
		},
	}

	applyWorkspaceSecurityDefaults(pod)

	require.NotNil(t, pod.Spec.AutomountServiceAccountToken)
	require.False(t, *pod.Spec.AutomountServiceAccountToken)
	require.NotNil(t, pod.Spec.EnableServiceLinks)
	require.False(t, *pod.Spec.EnableServiceLinks)

	require.NotNil(t, pod.Spec.SecurityContext)
	require.Equal(t, int64(1000), *pod.Spec.SecurityContext.RunAsUser)
	require.Equal(t, int64(3000), *pod.Spec.SecurityContext.RunAsGroup)
	require.True(t, *pod.Spec.SecurityContext.RunAsNonRoot)
	require.NotNil(t, pod.Spec.SecurityContext.SeccompProfile)
	require.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, pod.Spec.SecurityContext.SeccompProfile.Type)

	require.Len(t, pod.Spec.Containers, 1)
	securityContext := pod.Spec.Containers[0].SecurityContext
	require.NotNil(t, securityContext)
	require.True(t, *securityContext.RunAsNonRoot)
	require.False(t, *securityContext.Privileged)
	require.False(t, *securityContext.AllowPrivilegeEscalation)
	require.Equal(t, []corev1.Capability{"ALL"}, securityContext.Capabilities.Drop)
	require.Nil(t, securityContext.ReadOnlyRootFilesystem, "read-only rootfs requires a separate writable-mount rollout")

	require.Len(t, pod.Spec.InitContainers, 1)
	initSecurityContext := pod.Spec.InitContainers[0].SecurityContext
	require.NotNil(t, initSecurityContext)
	require.True(t, *initSecurityContext.RunAsNonRoot)
	require.False(t, *initSecurityContext.Privileged)
	require.False(t, *initSecurityContext.AllowPrivilegeEscalation)
	require.Equal(t, []corev1.Capability{"ALL"}, initSecurityContext.Capabilities.Drop)
}

func TestApplyWorkspaceSecurityDefaultsPreservesUnrelatedContainerSettings(t *testing.T) {
	t.Parallel()

	readOnlyRootFilesystem := true
	customRunAsUser := int64(2000)
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "workspace-server",
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &readOnlyRootFilesystem,
			RunAsUser:              &customRunAsUser,
		},
	}}}}

	applyWorkspaceSecurityDefaults(pod)

	securityContext := pod.Spec.Containers[0].SecurityContext
	require.True(t, *securityContext.ReadOnlyRootFilesystem)
	require.Equal(t, int64(2000), *securityContext.RunAsUser)
	require.True(t, *securityContext.RunAsNonRoot)
	require.False(t, *securityContext.Privileged)
	require.False(t, *securityContext.AllowPrivilegeEscalation)
	require.Equal(t, []corev1.Capability{"ALL"}, securityContext.Capabilities.Drop)
}

func TestApplyWorkspaceSecurityDefaultsPreservesUnrelatedPodSettings(t *testing.T) {
	t.Parallel()

	fsGroup := int64(4000)
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup:            &fsGroup,
			SupplementalGroups: []int64{5000},
		},
		Containers: []corev1.Container{{Name: "workspace-server"}},
	}}

	applyWorkspaceSecurityDefaults(pod)

	require.Equal(t, int64(4000), *pod.Spec.SecurityContext.FSGroup)
	require.Equal(t, []int64{5000}, pod.Spec.SecurityContext.SupplementalGroups)
	require.Equal(t, int64(1000), *pod.Spec.SecurityContext.RunAsUser)
	require.Equal(t, int64(3000), *pod.Spec.SecurityContext.RunAsGroup)
	require.True(t, *pod.Spec.SecurityContext.RunAsNonRoot)
	require.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, pod.Spec.SecurityContext.SeccompProfile.Type)
}

func TestWorkspaceSpecVersionAnnotation(t *testing.T) {
	t.Parallel()

	current := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		workspaceSpecAnnotationKey: workspaceSpecVersion,
	}}}
	old := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		workspaceSpecAnnotationKey: "old-version",
	}}}

	require.True(t, hasCurrentWorkspaceSpec(current))
	require.False(t, hasCurrentWorkspaceSpec(old))
	require.False(t, hasCurrentWorkspaceSpec(&corev1.Pod{}))
	require.False(t, hasCurrentWorkspaceSpec(nil))
}

func TestSelectWorkspaceSpecUpgrades(t *testing.T) {
	t.Parallel()

	pod := func(name, image, specVersion string) corev1.Pod {
		return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: map[string]string{
			imageAnnotationKey:         image,
			workspaceSpecAnnotationKey: specVersion,
		}}}
	}
	terminating := pod("terminating-old-spec", "image:v1", "")
	now := metav1.Now()
	terminating.DeletionTimestamp = &now

	pods := []corev1.Pod{
		pod("current", "image:v1", workspaceSpecVersion),
		terminating,
		pod("old-spec-1", "image:v1", ""),
		pod("old-image", "image:v0", ""),
		pod("old-spec-2", "image:v1", "old-version"),
		pod("old-spec-3", "image:v1", "old-version"),
	}

	selected := selectWorkspaceSpecUpgrades(pods, "image:v1", 2)
	require.Len(t, selected, 2)
	require.Equal(t, []string{"old-spec-1", "old-spec-2"}, []string{selected[0].Name, selected[1].Name})
	require.Nil(t, selectWorkspaceSpecUpgrades(pods, "image:v1", 0))
}
