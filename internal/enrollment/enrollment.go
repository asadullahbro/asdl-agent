package enrollment

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/asdl/agent/internal/config"
)

type EnrollRequest struct {
	Token              string   `json:"token"`
	Hostname           string   `json:"hostname"`
	WireGuardPublicKey string   `json:"wireguard_public_key"`
	OS                 string   `json:"os"`
	Arch               string   `json:"arch"`
	CPU                int      `json:"cpu"`
	MemoryTotal        int64    `json:"memory_total"`
	DiskTotal          int64    `json:"disk_total"`
	Capabilities       []string `json:"capabilities"`
	SSHUser            string   `json:"ssh_user"`
}

type EnrollResponse struct {
	NodeID               string `json:"node_id"`
	AssignedIP           string `json:"assigned_ip"`
	HubWireGuardPubKey   string `json:"hub_wireguard_public_key"`
	HubWireGuardEndpoint string `json:"hub_wireguard_endpoint"`
	SSHPublicKey         string `json:"ssh_public_key"`
}

func Run(configPath string) (*config.Config, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║        ASDL Hub Node Enrollment      ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Println()

	// Hub URL
	fmt.Print("Hub URL (e.g. https://hub.asdl.website): ")
	hubURL, _ := reader.ReadString('\n')
	hubURL = strings.TrimSpace(hubURL)
	if hubURL == "" {
		return nil, fmt.Errorf("hub URL is required")
	}

	// Enrollment token
	fmt.Print("Enrollment token: ")
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("enrollment token is required")
	}

	// SSH user
	defaultUser := os.Getenv("SUDO_USER")
	if defaultUser == "" {
		defaultUser = "ubuntu"
	}
	fmt.Printf("SSH user [%s]: ", defaultUser)
	sshUser, _ := reader.ReadString('\n')
	sshUser = strings.TrimSpace(sshUser)
	if sshUser == "" {
		sshUser = defaultUser
	}

	fmt.Println()
	fmt.Println("📦 Gathering system info...")

	hostname, _ := os.Hostname()
	goos := runtime.GOOS
	arch := runtime.GOARCH
	cpu := getCPUCount()
	memory := getMemoryTotal()
	disk := getDiskTotal()
	caps := detectCapabilities()

	fmt.Println("🔑 Generating WireGuard keypair...")
	privKey, pubKey, err := generateWireGuardKeys()
	if err != nil {
		return nil, fmt.Errorf("WireGuard keygen failed: %v", err)
	}

	fmt.Println("🤝 Enrolling with hub...")

	req := EnrollRequest{
		Token:              token,
		Hostname:           hostname,
		WireGuardPublicKey: pubKey,
		OS:                 goos,
		Arch:               arch,
		CPU:                cpu,
		MemoryTotal:        memory,
		DiskTotal:          disk,
		Capabilities:       caps,
		SSHUser:            sshUser,
	}

	resp, err := enroll(hubURL, req)
	if err != nil {
		return nil, fmt.Errorf("enrollment failed: %v", err)
	}

	fmt.Printf("✅ Enrolled! Node ID: %s\n", resp.NodeID)
	fmt.Printf("   Assigned IP: %s\n", resp.AssignedIP)

	fmt.Println()
	fmt.Println("🔧 Configuring WireGuard...")
	if err := configureWireGuard(privKey, resp); err != nil {
		return nil, fmt.Errorf("WireGuard config failed: %v", err)
	}
	fmt.Println("✅ WireGuard configured and started")

	// Add SSH public key
	if resp.SSHPublicKey != "" {
		fmt.Println("🔑 Adding SSH key for terminal access...")
		if err := addSSHKey(sshUser, resp.SSHPublicKey); err != nil {
			fmt.Printf("⚠️  SSH key setup failed (terminal won't work): %v\n", err)
		} else {
			fmt.Printf("✅ SSH key added for user: %s\n", sshUser)
		}
	}

	// Save config
	cfg := &config.Config{
		HubURL:   hubURL,
		VPNIP:    resp.AssignedIP,
		NodeID:   resp.NodeID,
		Interval: 30 * time.Second,
		WorkDir:  "/tmp/asdl",
		MaxJobs:  5,
		Enrolled: true,
	}

	if err := cfg.Save(configPath); err != nil {
		fmt.Printf("⚠️  Config save failed: %v\n", err)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║         Enrollment Complete! ✅       ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("   Node ID:  %s\n", resp.NodeID)
	fmt.Printf("   VPN IP:   %s\n", resp.AssignedIP)
	fmt.Printf("   Hub:      %s\n", hubURL)
	fmt.Println()

	return cfg, nil
}

func enroll(hubURL string, req EnrollRequest) (*EnrollResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(
		hubURL+"/api/v1/enrollment/enroll",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		var errResp struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("hub returned %d: %s", resp.StatusCode, errResp.Error)
	}

	var enrollResp EnrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&enrollResp); err != nil {
		return nil, err
	}
	return &enrollResp, nil
}

func generateWireGuardKeys() (privateKey, publicKey string, err error) {
	// Generate private key
	privOut, err := exec.Command("wg", "genkey").Output()
	if err != nil {
		return "", "", fmt.Errorf("wg genkey failed: %v", err)
	}
	privateKey = strings.TrimSpace(string(privOut))

	// Derive public key
	pubCmd := exec.Command("wg", "pubkey")
	pubCmd.Stdin = strings.NewReader(privateKey)
	pubOut, err := pubCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("wg pubkey failed: %v", err)
	}
	publicKey = strings.TrimSpace(string(pubOut))
	return privateKey, publicKey, nil
}

func configureWireGuard(privateKey string, resp *EnrollResponse) error {
	conf := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/24
DNS = 1.1.1.1

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = 10.100.0.0/24
PersistentKeepalive = 25
`, privateKey, resp.AssignedIP, resp.HubWireGuardPubKey, resp.HubWireGuardEndpoint)

	if err := os.MkdirAll("/etc/wireguard", 0700); err != nil {
		return err
	}
	if err := os.WriteFile("/etc/wireguard/wg0.conf", []byte(conf), 0600); err != nil {
		return err
	}

	// Enable and start
	exec.Command("systemctl", "enable", "wg-quick@wg0").Run()
	out, err := exec.Command("systemctl", "start", "wg-quick@wg0").CombinedOutput()
	if err != nil {
		// Try restart in case it's already running
		out, err = exec.Command("systemctl", "restart", "wg-quick@wg0").CombinedOutput()
		if err != nil {
			return fmt.Errorf("wg-quick start failed: %v — %s", err, string(out))
		}
	}

	// Wait for interface to come up
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if ip := getWireGuardIP(); ip == resp.AssignedIP {
			return nil
		}
	}
	return nil
}

func addSSHKey(sshUser, pubKey string) error {
	homeDir := "/home/" + sshUser
	if sshUser == "root" {
		homeDir = "/root"
	}

	sshDir := homeDir + "/.ssh"
	authFile := sshDir + "/authorized_keys"

	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}

	f, err := os.OpenFile(authFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(pubKey + "\n")
	return err
}

func getWireGuardIP() string {
	iface, err := net.InterfaceByName("wg0")
	if err != nil {
		return ""
	}
	addrs, _ := iface.Addrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ip := ipnet.IP.To4(); ip != nil {
				return ip.String()
			}
		}
	}
	return ""
}

func getCPUCount() int {
	out, err := exec.Command("nproc").Output()
	if err != nil {
		return 1
	}
	var count int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count)
	return count
}

func getMemoryTotal() int64 {
	out, err := exec.Command("sh", "-c", "free -b | awk '/Mem:/{print $2}'").Output()
	if err != nil {
		return 0
	}
	var mem int64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &mem)
	return mem
}

func getDiskTotal() int64 {
	out, err := exec.Command("sh", "-c", "df -B1 / | awk 'NR==2{print $2}'").Output()
	if err != nil {
		return 0
	}
	var disk int64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &disk)
	return disk
}

func detectCapabilities() []string {
	caps := []string{}
	if _, err := exec.LookPath("docker"); err == nil {
		caps = append(caps, "docker")
	}
	if _, err := exec.LookPath("git"); err == nil {
		caps = append(caps, "git")
	}
	return caps
}