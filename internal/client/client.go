package client

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "sync"
    "time"

    "github.com/asdl/agent/pkg/models"
)

// The Hub tells nodes apart by the mesh address their requests come from, so
// the agent never needs (or keeps) a node ID. Routes that used to carry the ID
// get this placeholder instead; the Hub ignores that segment.
const self = "self"

type Client struct {
    baseURL    string
    vpnIP      string  // ADD THIS FIELD
    mu         sync.RWMutex
    registered bool
    httpClient *http.Client
}

func NewClient(baseURL string, vpnIP string) *Client {  // ADD vpnIP parameter
    return &Client{
        baseURL: baseURL,
        vpnIP:   vpnIP,  // Store it
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
        },
    }
}

func (c *Client) isRegistered() bool {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.registered
}

func (c *Client) setRegistered() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.registered = true
}

func (c *Client) Register(info *models.NodeInfo) error {
    url := c.baseURL + "/api/v1/nodes"

    // Set VPN IP from client config
    info.VPNIP = c.vpnIP

    data, err := json.Marshal(info)
    if err != nil {
        return err
    }

    req, err := http.NewRequest("POST", url, bytes.NewReader(data))
    if err != nil {
        return err
    }

    req.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("registration failed (status %d): %s", resp.StatusCode, string(body))
    }

    // The reply describes the node as the Hub knows it, including its ID,
    // which this side has no use for.
    io.Copy(io.Discard, resp.Body)

    c.setRegistered()
    return nil
}

func (c *Client) SendHeartbeat(heartbeat *models.Heartbeat) (*models.HeartbeatReply, error) {
    if !c.isRegistered() {
        return nil, fmt.Errorf("node not registered")
    }

    url := fmt.Sprintf("%s/api/v1/nodes/%s/heartbeat", c.baseURL, self)

    data, err := json.Marshal(heartbeat)
    if err != nil {
        return nil, err
    }

    req, err := http.NewRequest("POST", url, bytes.NewReader(data))
    if err != nil {
        return nil, err
    }

    req.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, string(body))
    }

    var reply models.HeartbeatReply
    // Older Hubs answer without a body worth reading; that's fine.
    _ = json.NewDecoder(resp.Body).Decode(&reply)
    return &reply, nil
}

// SetMaintenance asks the Hub to put this node into maintenance (or take it
// out). The Hub moves the node's apps elsewhere when it goes in.
func (c *Client) SetMaintenance(enabled bool) (*models.MaintenanceResult, error) {
    if !c.isRegistered() {
        return nil, fmt.Errorf("node not registered")
    }
    url := fmt.Sprintf("%s/api/v1/nodes/%s/maintenance", c.baseURL, self)
    data, _ := json.Marshal(map[string]bool{"enabled": enabled})
    req, err := http.NewRequest("POST", url, bytes.NewReader(data))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    var res models.MaintenanceResult
    _ = json.NewDecoder(resp.Body).Decode(&res)
    if resp.StatusCode == http.StatusNotFound {
        return nil, fmt.Errorf("this Hub doesn't support maintenance mode yet (update it to v0.7.0 or later)")
    }
    if resp.StatusCode != http.StatusOK {
        if res.Error != "" {
            return nil, fmt.Errorf("%s", res.Error)
        }
        return nil, fmt.Errorf("the Hub answered %d", resp.StatusCode)
    }
    return &res, nil
}

func (c *Client) ClaimJob() (*models.Job, error) {
    if !c.isRegistered() {
        return nil, fmt.Errorf("node not registered")
    }

    url := c.baseURL + "/api/v1/jobs/claim"

    req, err := http.NewRequest("POST", url, nil)
    if err != nil {
        return nil, err
    }

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusNoContent {
        return nil, nil
    }

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("claim failed (status %d): %s", resp.StatusCode, string(body))
    }

    var job models.Job
    if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
        return nil, err
    }

    return &job, nil
}

func (c *Client) CompleteJob(result *models.JobResult) error {
    url := fmt.Sprintf("%s/api/v1/jobs/%s/complete", c.baseURL, result.JobID)

    data, err := json.Marshal(result)
    if err != nil {
        return err
    }

    req, err := http.NewRequest("POST", url, bytes.NewReader(data))
    if err != nil {
        return err
    }

    req.Header.Set("Content-Type", "application/json")

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("completion failed (status %d): %s", resp.StatusCode, string(body))
    }

    return nil
}