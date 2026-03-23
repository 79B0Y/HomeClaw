# Xray Proxy Manager API Documentation

This directory provides the backend server (`xray-panel-api.py`) for managing the `xray-proxy` service via a REST JSON API.

## Endpoints

### 1. Status Check
Check the running status of the Xray proxy service.
- **Path:** `/api/status`
- **Method:** `GET`
- **Response Format:** JSON
- **Success Response:**
  ```json
  {
    "status": "running",
    "message": "active"
  }
  ```
  *(Status will be `"stopped"` if the process is down, or `"error"` if unreachable).*

### 2. Service Control
Start, stop, or restart the proxy service securely.
- **Path:** `/api/service/<action>`
- **Method:** `POST`
- **Parameters:** `<action>` must be `start`, `stop`, or `restart`.
- **Response Format:** JSON
- **Success Response (200 OK):**
  ```json
  {
    "status": "success",
    "message": "Service started successfully"
  }
  ```

### 3. Subscription & Configuration (Panel API)
- These additional endpoints (e.g. `/api/subscribe`, `/api/apply`, `/api/settings`) handle application configurations. Support to be fully mapped internally to `xray-panel.html` frontend handlers.

---
**Core Control Architecture:** 
The backend leverages `subprocess` to trigger actions against `systemctl`. Modifying the state of `xray-proxy` dynamically controls node execution.