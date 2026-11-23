package workspace

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"nudgebee/llm/config"
)

const (
	workspaceProxyCAKeyKey  = "ca.key"
	workspaceProxyCAValid   = 10 * 365 * 24 * time.Hour
	workspaceProxyLeafValid = 90 * 24 * time.Hour
)

// WorkspaceProxyCertificates returns the certificate source for the
// workspace proxy's TLS port: a certificate for WorkspaceProxyTLSHost signed by
// the proxy's own CA. The CA
// lives in a Secret in the workspace namespace, created by the first replica
// that needs it (create, or read it if another replica won the race);
// workspace pods mount only its public certificate. Each replica signs its
// own certificate and re-signs it before it expires, so a local llm-server can
// serve its own name and a long-running replica never serves an expired one.
func WorkspaceProxyCertificates(ctx context.Context) (func(*tls.ClientHelloInfo) (*tls.Certificate, error), error) {
	names := workspaceProxyNames(workspaceProxyHostname())
	caCert, caKey, err := loadOrCreateWorkspaceProxyCA(ctx, names)
	if err != nil {
		return nil, err
	}
	src := &workspaceProxyCertSource{ca: caCert, caKey: caKey, names: names}
	cert, err := src.current(time.Now())
	if err != nil {
		return nil, err
	}
	if err := verifyWorkspaceProxyLeaf(caCert, cert, names[0]); err != nil {
		return nil, err
	}
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return src.current(time.Now()) }, nil
}

type workspaceProxyCertSource struct {
	mu      sync.Mutex
	ca      *x509.Certificate
	caKey   *ecdsa.PrivateKey
	names   []string
	cert    *tls.Certificate
	expires time.Time
}

// current returns the certificate, re-signing it a week before it expires.
func (s *workspaceProxyCertSource) current(now time.Time) (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cert != nil && now.Before(s.expires.Add(-7*24*time.Hour)) {
		return s.cert, nil
	}
	cert, err := signWorkspaceProxyLeaf(s.ca, s.caKey, s.names, now)
	if err != nil {
		return nil, err
	}
	s.cert, s.expires = cert, now.Add(workspaceProxyLeafValid)
	return cert, nil
}

// workspaceProxyHostname is WorkspaceProxyTLSHost without its port; the configured host may
// be given with or without one.
func workspaceProxyHostname() string {
	hostport := WorkspaceProxyTLSHost()
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

// workspaceProxyNames is the proxy host plus its in-cluster service forms, so the
// certificate holds however a workspace pod reaches llm-server.
func workspaceProxyNames(host string) []string {
	names := []string{host}
	if net.ParseIP(host) != nil {
		return names
	}
	ns := config.Config.LlmServerCodeAgentNamespace
	if ns != "" && !strings.Contains(host, ".") {
		names = append(names, host+"."+ns, host+"."+ns+".svc", host+"."+ns+".svc.cluster.local")
	}
	return names
}

// workspaceProxySecretName is the Secret holding the CA for this proxy host. The CA
// is name-constrained to the host, so each host gets its own Secret, named
// from the host: an llm-server reached under another name (a local run against
// the same cluster) creates its own CA instead of failing on another's.
func workspaceProxySecretName() string {
	sum := sha256.Sum256([]byte(strings.ToLower(workspaceProxyHostname())))
	return "llm-server-workspace-proxy-" + hex.EncodeToString(sum[:])[:10]
}

// verifyWorkspaceProxyLeaf fails when the CA does not allow this replica's proxy
// name: a CA created for another host is name-constrained to that host's
// names, and workspace commands would reject the certificate.
func verifyWorkspaceProxyLeaf(ca *x509.Certificate, cert *tls.Certificate, name string) error {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("workspace proxy tls: leaf cert: %w", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots}); err != nil {
		return fmt.Errorf("workspace proxy tls: the CA in Secret %s does not allow %q; delete the Secret to rotate the CA: %w",
			workspaceProxySecretName(), name, err)
	}
	return nil
}

func loadOrCreateWorkspaceProxyCA(ctx context.Context, names []string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	clientset, err := getKubeClient(5, 10)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: kube client: %w", err)
	}
	ns := config.Config.LlmServerCodeAgentNamespace
	name := workspaceProxySecretName()
	secrets := clientset.CoreV1().Secrets(ns)

	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		certPEM, keyPEM, genErr := newWorkspaceProxyCA(time.Now(), names)
		if genErr != nil {
			return nil, nil, genErr
		}
		secret, err = secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": "llm-server", "component": "workspace-proxy"}},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{workspaceProxyCAKey: certPEM, workspaceProxyCAKeyKey: keyPEM},
		}, metav1.CreateOptions{})
		if errors.IsAlreadyExists(err) {
			secret, err = secrets.Get(ctx, name, metav1.GetOptions{})
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: ca secret %s/%s: %w", ns, name, err)
	}
	return parseWorkspaceProxyCA(secret.Data[workspaceProxyCAKey], secret.Data[workspaceProxyCAKeyKey])
}

// newWorkspaceProxyCA creates the proxy's CA, name-constrained to the proxy's
// names: even if its key leaks, it cannot sign a certificate a workspace
// command would accept for any other host (github.com included).
//
// Rotation: delete the Secret, then restart every llm-server replica (they
// read the CA only at start). The first to start creates a new CA; workspace
// pods see it without a restart because the volume has no subPath.
func newWorkspaceProxyCA(now time.Time, names []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: ca key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "Nudgebee workspace proxy CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(workspaceProxyCAValid),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	tmpl.PermittedDNSDomainsCritical = true
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			bits := 8 * len(ip.To16())
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			tmpl.PermittedIPRanges = append(tmpl.PermittedIPRanges, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		} else {
			tmpl.PermittedDNSDomains = append(tmpl.PermittedDNSDomains, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: ca cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: ca key encode: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func parseWorkspaceProxyCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: ca secret is malformed")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: ca cert: %w", err)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace proxy tls: ca key: %w", err)
	}
	return cert, key, nil
}

func signWorkspaceProxyLeaf(ca *x509.Certificate, caKey *ecdsa.PrivateKey, names []string, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("workspace proxy tls: leaf key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(workspaceProxyLeafValid),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("workspace proxy tls: leaf cert: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func randomSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// workspaceProxyCAVolume mounts only the CA's public certificate into a workspace
// pod. Optional: a pod starts without it when the proxy has never run.
func workspaceProxyCAVolume() (corev1.Volume, corev1.VolumeMount) {
	optional := true
	return corev1.Volume{
			Name: "workspace-proxy-ca",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: workspaceProxySecretName(),
				Items:      []corev1.KeyToPath{{Key: workspaceProxyCAKey, Path: workspaceProxyCAKey}},
				Optional:   &optional,
			}},
		}, corev1.VolumeMount{
			Name:      "workspace-proxy-ca",
			MountPath: workspaceProxyCADir,
			ReadOnly:  true,
		}
}
