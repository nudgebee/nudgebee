package vmservicemap

// The structs below are the forager `vm_service_map` response; see CONTRACT.md.

// Window is the time range every series in a Response was evaluated over.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Listener is one TCP listen address (container_net_tcp_listen_info).
type Listener struct {
	Addr string `json:"addr"`
	Port int    `json:"port"`
}

// Host is a node-agent that reported inside the window.
type Host struct {
	InstanceID string     `json:"instance_id"`
	Hostname   string     `json:"hostname"`
	IPs        []string   `json:"ips"`
	Listeners  []Listener `json:"listeners"`
}

// Service is one active systemd unit or container on a host.
type Service struct {
	InstanceID       string     `json:"instance_id"`
	Hostname         string     `json:"hostname"`
	ContainerID      string     `json:"container_id"`
	Name             string     `json:"name"`
	Kind             string     `json:"kind"` // systemd, docker, other
	Image            string     `json:"image"`
	ApplicationTypes []string   `json:"application_types"`
	Listeners        []Listener `json:"listeners"`
}

// Edge is one caller-side connection aggregate: a service to a destination.
type Edge struct {
	SourceHost        string `json:"source_host"`
	SourceInstanceID  string `json:"source_instance_id"`
	SourceService     string `json:"source_service"`
	SourceContainerID string `json:"source_container_id"`

	Destination     string `json:"destination"`
	DestinationKind string `json:"destination_kind"`
	DestinationName string `json:"destination_name"`

	// Set by the forager when the destination belongs to a reporting host: that
	// host, and the service listening on the port there.
	DestinationHost        string `json:"destination_host"`
	DestinationInstanceID  string `json:"destination_instance_id"`
	DestinationService     string `json:"destination_service"`
	DestinationContainerID string `json:"destination_container_id"`

	Protocol      string  `json:"protocol"`
	Requests      float64 `json:"requests"`
	Errors        float64 `json:"errors"`
	LatencyMs     float64 `json:"latency_ms"`
	BytesSent     float64 `json:"bytes_sent"`
	BytesReceived float64 `json:"bytes_received"`
}

// Response is the decoded forager answer.
type Response struct {
	Window    Window    `json:"window"`
	Hosts     []Host    `json:"hosts"`
	Services  []Service `json:"services"`
	Edges     []Edge    `json:"edges"`
	Truncated bool      `json:"truncated"`
}

// Destination kinds emitted by the node-agent (common/vm_resolver.go).
const (
	KindVM       = "vm"
	KindPrivate  = "private"
	KindExternal = "external"
)

// Node kinds in the built map (ServiceApplicationId.Kind).
const (
	NodeKindSystemd         = "SystemdService"
	NodeKindDocker          = "DockerContainer"
	NodeKindProcess         = "Process"
	NodeKindUnknownProcess  = "UnknownProcess"
	NodeKindUnknownInternal = "UnknownInternalHost"
	NodeKindExternal        = "ExternalService"
)
