#!/bin/bash
set -e

APP_NAME="sinkhole"
BIN_DIR="/usr/local/bin"
SERVICE_FILE="deploy/go-sinkhole.service"
SYSTEMD_DIR="/etc/systemd/system"

# Colors
GREEN='\033[0;32m'
NC='\033[0m'

echo -e "${GREEN}Building Go-Sinkhole...${NC}"
go build -o $APP_NAME ./cmd/sinkhole

echo -e "${GREEN}Stopping Service (if running)...${NC}"
sudo systemctl stop go-sinkhole || true

echo -e "${GREEN}Installing binary to $BIN_DIR...${NC}"
sudo cp $APP_NAME $BIN_DIR/
sudo chmod +x $BIN_DIR/$APP_NAME

echo -e "${GREEN}Installing Systemd Service...${NC}"
if [ -f "$SERVICE_FILE" ]; then
    sudo cp $SERVICE_FILE $SYSTEMD_DIR/
    sudo systemctl daemon-reload
else
    echo "Error: Service file $SERVICE_FILE not found!"
    exit 1
fi

echo -e "${GREEN}Enabling and Starting Service...${NC}"
sudo systemctl enable go-sinkhole
sudo systemctl restart go-sinkhole

echo -e "${GREEN}Installation Complete!${NC}"
echo "Run 'sinkhole tui' to monitor the service."
echo "Logs: /var/log/go-sinkhole.log"
