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

type Client struct {
    baseURL    string
    nodeID     string
    vpnIP      string  // ADD THIS FIELD
    mu         sync.RWMutex
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

func (c *Client) GetNodeID() string {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.nodeID
}

func (c *Client) setNodeID(id string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.nodeID = id
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

    var node models.NodeInfo
    if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
        return err
    }

    c.setNodeID(node.ID)
    return nil
}

func (c *Client) SendHeartbeat(heartbeat *models.Heartbeat) error {
    nodeID := c.GetNodeID()
    if nodeID == "" {
        return fmt.Errorf("node not registered")
    }

    url := fmt.Sprintf("%s/api/v1/nodes/%s/heartbeat", c.baseURL, nodeID)

    data, err := json.Marshal(heartbeat)
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
        return fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, string(body))
    }

    return nil
}

func (c *Client) ClaimJob() (*models.Job, error) {
    nodeID := c.GetNodeID()
    if nodeID == "" {
        return nil, fmt.Errorf("node not registered")
    }

    url := fmt.Sprintf("%s/api/v1/jobs/claim?node_id=%s", c.baseURL, nodeID)

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