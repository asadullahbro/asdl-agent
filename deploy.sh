cat > ~/deploy-agents.sh << 'EOF'
#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Deploying ASDL Agents to All Nodes${NC}"
echo -e "${GREEN}========================================${NC}"

# List of nodes: username@ip
NODES=(
    "asadullah@10.100.0.2"
    "abdullah@10.100.0.3"
    "abdullah@10.100.0.4"
)

AGENT_BIN="./bin/asdl-agent-linux"

for node in "${NODES[@]}"; do
    USER=$(echo $node | cut -d'@' -f1)
    IP=$(echo $node | cut -d'@' -f2)
    
    echo -e "\n${YELLOW}>>> Deploying to $node ${NC}"
    
    # Determine home directory
    HOME_DIR="/home/$USER"
    
    # Copy binary
    echo "📤 Copying agent binary..."
    scp $AGENT_BIN $node:$HOME_DIR/asdl-agent/asdl-agent
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✅ Binary copied successfully${NC}"
    else
        echo -e "${RED}❌ Failed to copy binary${NC}"
        continue
    fi
    
    # Make executable and restart service
    echo "🔄 Restarting agent service..."
    ssh -t $node "sudo chmod +x $HOME_DIR/asdl-agent/asdl-agent && sudo systemctl restart asdl-agent && sudo systemctl status asdl-agent --no-pager"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✅ Agent restarted successfully${NC}"
    else
        echo -e "${RED}❌ Failed to restart agent${NC}"
    fi
done

echo -e "\n${GREEN}========================================${NC}"
echo -e "${GREEN}Deployment Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
EOF

chmod +x ~/deploy-agents.sh