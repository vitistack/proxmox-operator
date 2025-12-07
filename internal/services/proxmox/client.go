package proxmox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/luthermonson/go-proxmox"
	vitistackcrdsv1alpha1 "github.com/vitistack/common/pkg/v1alpha1"
)

// ClientOption is a functional option for configuring the Proxmox client
type ClientOption func(*clientConfig)

type clientConfig struct {
	endpoint    string
	username    string
	password    string
	insecure    bool
	tokenID     string // For token-based auth
	tokenSecret string
}

// WithEndpoint sets the Proxmox API endpoint
func WithEndpoint(endpoint string) ClientOption {
	return func(c *clientConfig) {
		c.endpoint = endpoint
	}
}

// WithCredentials sets username and password
func WithCredentials(username, password string) ClientOption {
	return func(c *clientConfig) {
		c.username = username
		c.password = password
	}
}

// WithToken sets token for authentication (tokenID and secret)
func WithToken(tokenID, tokenSecret string) ClientOption {
	return func(c *clientConfig) {
		c.tokenID = tokenID
		c.tokenSecret = tokenSecret
	}
}

// WithInsecureTLS sets whether to skip TLS verification
func WithInsecureTLS(insecure bool) ClientOption {
	return func(c *clientConfig) {
		c.insecure = insecure
	}
}

// ProxmoxClient defines the interface for interacting with Proxmox
type ProxmoxClient interface {
	CreateVM(ctx context.Context, node string, vmID int, options []proxmox.VirtualMachineOption) (*proxmox.Task, error)
	GetVM(ctx context.Context, node string, vmID int) (*proxmox.VirtualMachine, error)
	DeleteVM(ctx context.Context, node string, vmID int) (*proxmox.Task, error)
	StartVM(ctx context.Context, node string, vmID int) (*proxmox.Task, error)
	StopVM(ctx context.Context, node string, vmID int) (*proxmox.Task, error)
	GetNodeNames(ctx context.Context) ([]string, error)
	TestConnection(ctx context.Context) error
	Client() *proxmox.Client
}

// proxmoxClient implements ProxmoxClient
type proxmoxClient struct {
	client *proxmox.Client
}

// NewProxmoxClient creates a new Proxmox client using functional options
func NewProxmoxClient(opts ...ClientOption) (ProxmoxClient, error) {
	config := &clientConfig{}

	for _, opt := range opts {
		opt(config)
	}

	// Validate required fields
	if config.endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if config.tokenID == "" && config.username == "" {
		return nil, fmt.Errorf("authentication is required (either token or username/password)")
	}

	var clientOpts []proxmox.Option

	// Set up authentication
	if config.tokenID != "" && config.tokenSecret != "" {
		clientOpts = append(clientOpts, proxmox.WithAPIToken(config.tokenID, config.tokenSecret))
	} else if config.username != "" && config.password != "" {
		credentials := proxmox.Credentials{
			Username: config.username,
			Password: config.password,
		}
		clientOpts = append(clientOpts, proxmox.WithCredentials(&credentials))
	} else {
		return nil, fmt.Errorf("either token (ID and secret) or username/password must be provided")
	}

	// Set up HTTP client for TLS config
	if config.insecure {
		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // #nosec G402
				},
			},
		}
		clientOpts = append(clientOpts, proxmox.WithHTTPClient(httpClient))
	}

	client := proxmox.NewClient(config.endpoint, clientOpts...)

	return &proxmoxClient{
		client: client,
	}, nil
}

// NewProxmoxClientFromConfig creates a new Proxmox client from config values
func NewProxmoxClientFromConfig(endpoint, username, password string, insecure bool) (ProxmoxClient, error) {
	return NewProxmoxClient(
		WithEndpoint(endpoint),
		WithCredentials(username, password),
		WithInsecureTLS(insecure),
	)
}

// NewProxmoxClientFromProvider creates a Proxmox client from MachineProvider spec
func NewProxmoxClientFromProvider(provider *vitistackcrdsv1alpha1.MachineProvider) (ProxmoxClient, error) {
	if provider.Spec.ProviderType != string(vitistackcrdsv1alpha1.MachineProviderTypeProxmox) {
		return nil, fmt.Errorf("provider type is not proxmox")
	}

	endpoint := provider.Spec.Endpoint.URL
	insecure := provider.Spec.Endpoint.InsecureSkipVerify

	// Check authentication type
	var client ProxmoxClient
	var err error

	switch provider.Spec.Authentication.Type {
	case "token":
		tokenID := provider.Spec.Authentication.Parameters["tokenID"]
		tokenSecret := provider.Spec.Authentication.Parameters["tokenSecret"]
		if tokenID == "" || tokenSecret == "" {
			return nil, fmt.Errorf("tokenID and tokenSecret required for token authentication")
		}
		client, err = NewProxmoxClient(
			WithEndpoint(endpoint),
			WithToken(tokenID, tokenSecret),
			WithInsecureTLS(insecure),
		)
	case "credentials":
		fallthrough
	default:
		username := provider.Spec.Authentication.Parameters["username"]
		password := provider.Spec.Authentication.Parameters["password"]
		if username == "" || password == "" {
			return nil, fmt.Errorf("username and password required for credentials authentication")
		}
		client, err = NewProxmoxClient(
			WithEndpoint(endpoint),
			WithCredentials(username, password),
			WithInsecureTLS(insecure),
		)
	}

	return client, err
}

func (c *proxmoxClient) CreateVM(ctx context.Context, node string, vmID int, options []proxmox.VirtualMachineOption) (*proxmox.Task, error) {
	n, err := c.client.Node(ctx, node)
	if err != nil {
		return nil, err
	}
	return n.NewVirtualMachine(ctx, vmID, options...)
}

func (c *proxmoxClient) GetVM(ctx context.Context, node string, vmID int) (*proxmox.VirtualMachine, error) {
	n, err := c.client.Node(ctx, node)
	if err != nil {
		return nil, err
	}
	return n.VirtualMachine(ctx, vmID)
}

func (c *proxmoxClient) DeleteVM(ctx context.Context, node string, vmID int) (*proxmox.Task, error) {
	vm, err := c.GetVM(ctx, node, vmID)
	if err != nil {
		return nil, err
	}
	return vm.Delete(ctx)
}

func (c *proxmoxClient) StartVM(ctx context.Context, node string, vmID int) (*proxmox.Task, error) {
	vm, err := c.GetVM(ctx, node, vmID)
	if err != nil {
		return nil, err
	}
	return vm.Start(ctx)
}

func (c *proxmoxClient) StopVM(ctx context.Context, node string, vmID int) (*proxmox.Task, error) {
	vm, err := c.GetVM(ctx, node, vmID)
	if err != nil {
		return nil, err
	}
	return vm.Stop(ctx)
}

func (c *proxmoxClient) GetNodeNames(ctx context.Context) ([]string, error) {
	nodes, err := c.client.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(nodes))
	for i, node := range nodes {
		names[i] = node.Node
	}
	return names, nil
}

func (c *proxmoxClient) TestConnection(ctx context.Context) error {
	_, err := c.client.Nodes(ctx)
	return err
}

func (c *proxmoxClient) Client() *proxmox.Client {
	return c.client
}
