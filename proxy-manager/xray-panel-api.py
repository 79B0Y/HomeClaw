#!/usr/bin/env python3
"""
xray-panel-api.py  —  Xray 管理面板后端
依赖：sudo apt install python3-flask -y
运行：sudo python3 xray-panel-api.py
访问：http://设备IP:8080
"""

import json, os, re, subprocess, time
from flask import Flask, jsonify, request, send_file

app = Flask(__name__)

CONFIG_FILE = "/etc/xray-config.json"
SERVICE = "xray-proxy"

# ── 工具 ─────────────────────────────────────────────────────────────────────


def run(cmd, timeout=10):
    try:
        r = subprocess.run(
            cmd, shell=True, capture_output=True, text=True, timeout=timeout
        )
        return r.stdout.strip(), r.stderr.strip(), r.returncode
    except subprocess.TimeoutExpired:
        return "", "timeout", 1


def read_config():
    try:
        with open(CONFIG_FILE) as f:
            return json.load(f)
    except Exception:
        return None


def write_config(cfg):
    with open(CONFIG_FILE, "w") as f:
        json.dump(cfg, f, indent=2)


def get_ss_server(cfg):
    try:
        return cfg["outbounds"][0]["settings"]["servers"][0]
    except Exception:
        return None


def resolve_doh(domain):
    out, _, rc = run(
        "curl -s --max-time 5 'https://dns.alidns.com/resolve?name=%s&type=A'" % domain
    )
    m = re.findall(r'"data":"(\d+\.\d+\.\d+\.\d+)"', out)
    return m[-1] if m else None


# ── 订阅解析 ─────────────────────────────────────────────────────────────────


def parse_ssa_json(raw):
    """SSA JSON 数组格式：?list=ssa"""
    skip = ["流量", "时间", "网址", "套餐", "到期", "剩余", "过期"]
    nodes = []
    try:
        arr = json.loads(raw)
        for n in arr:
            r = n.get("remarks", "")
            if any(k in r for k in skip):
                continue
            s = n.get("server", "")
            p = n.get("server_port", "")
            pw = n.get("password", "")
            m = n.get("method", "chacha20-ietf-poly1305")
            if s and p and pw:
                nodes.append(
                    {
                        "name": r,
                        "server": s,
                        "port": int(p),
                        "password": pw,
                        "method": m,
                    }
                )
    except Exception:
        pass
    return nodes


def parse_clash_yaml(raw):
    """
    Clash YAML 格式：?clash=1
    兼容以下格式：
      - 带全局配置头（port: 7890, socks-port: 7891...）的完整 Clash 配置文件
      - JSON inline：  - {"name":"香港-01","type":"ss","server":"...","port":57001,...}
      - YAML flow：    - {name: 香港-01, type: ss, server: ..., cipher: ..., password: ...}
      - 多行展开格式
    """
    skip_kw = ["流量", "时间", "网址", "套餐", "到期", "剩余", "过期", "最新"]
    nodes = []

    # 找到顶层 proxies: 块，截止到下一个顶层 key 或文件末尾
    m = re.search(r"(?m)^proxies:\s*\n(.*?)(?=^[a-zA-Z][\w-]*\s*:|\Z)", raw, re.S)
    block = m.group(1) if m else raw

    # 逐行解析，每行一个节点（JSON inline 或 YAML flow）
    for line in block.splitlines():
        line = line.strip()
        if not line.startswith("- "):
            continue
        entry = line[2:].strip()

        # JSON inline 格式：- {"name":"香港-01","type":"ss",...}
        if entry.startswith("{"):
            try:
                n = json.loads(entry)
                if n.get("type", "").lower() not in ("ss", "shadowsocks"):
                    continue
                name = n.get("name", "")
                if any(k in name for k in skip_kw):
                    continue
                server = n.get("server", "")
                port = n.get("port", 0)
                passwd = n.get("password", "")
                cipher = n.get("cipher", "chacha20-ietf-poly1305")
                if server and port and passwd:
                    nodes.append(
                        {
                            "name": name,
                            "server": server,
                            "port": int(port),
                            "password": passwd,
                            "method": cipher,
                        }
                    )
            except Exception:
                pass
            continue

        # YAML flow 格式：- {name: 香港-01, type: ss, ...}
        typ_m = re.search(r'\btype:\s*["\']?(\S+?)["\']?(?:,|\})', entry)
        if not typ_m or typ_m.group(1).lower() not in ("ss", "shadowsocks"):
            continue

        def gf(key, e=entry):
            km = re.search(r"\b" + key + r':\s*["\']?([^,"\'\}]+)["\']?', e)
            return km.group(1).strip() if km else ""

        name = gf("name")
        if any(k in name for k in skip_kw):
            continue
        server = gf("server")
        port = gf("port")
        passwd = gf("password")
        cipher = gf("cipher")
        if server and port and passwd and cipher:
            try:
                nodes.append(
                    {
                        "name": name,
                        "server": server,
                        "port": int(port),
                        "password": passwd,
                        "method": cipher,
                    }
                )
            except ValueError:
                pass

    if nodes:
        return nodes

    # 回退：多行展开格式
    entries = re.split(r"\n(?=\s{0,4}-\s)", block)
    for e in entries:
        typ = re.search(r"type:\s*(\S+)", e)
        if not typ or typ.group(1).lower() not in ("ss", "shadowsocks"):
            continue
        name = re.search(r'name:\s*["\']?([^"\'\n]+)["\']?', e)
        server = re.search(r"server:\s*(\S+)", e)
        port = re.search(r"port:\s*(\d+)", e)
        passwd = re.search(r'password:\s*["\']?([^"\'\n]+)["\']?', e)
        cipher = re.search(r"cipher:\s*(\S+)", e)
        if all([name, server, port, passwd, cipher]):
            n = name.group(1).strip()
            if any(k in n for k in skip_kw):
                continue
            nodes.append(
                {
                    "name": n,
                    "server": server.group(1),
                    "port": int(port.group(1)),
                    "password": passwd.group(1).strip(),
                    "method": cipher.group(1),
                }
            )
    return nodes


# ── 节点测试（返回延迟 ms）───────────────────────────────────────────────────


def test_node_connectivity(addr, port, password, method, test_port=19990):
    """临时启动 xray 测试节点，返回 (out_ip, latency_ms) 或 (None, None)"""
    cfg = {
        "log": {"loglevel": "none"},
        "inbounds": [
            {"port": test_port, "protocol": "socks", "settings": {"auth": "noauth"}}
        ],
        "outbounds": [
            {
                "protocol": "shadowsocks",
                "settings": {
                    "servers": [
                        {
                            "address": addr,
                            "port": port,
                            "method": method,
                            "password": password,
                        }
                    ]
                },
                "tag": "proxy",
            }
        ],
    }
    tmp = "/tmp/xray-test-%d.json" % test_port
    with open(tmp, "w") as f:
        json.dump(cfg, f)

    proc = subprocess.Popen(
        ["/usr/local/bin/xray", "run", "-c", tmp],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    time.sleep(2)

    t0 = time.time()
    out, _, rc = run(
        "curl -s --max-time 8 --proxy socks5://127.0.0.1:%d https://api.ipify.org"
        % test_port,
        timeout=12,
    )
    latency = int((time.time() - t0) * 1000)

    proc.kill()
    proc.wait()
    try:
        os.remove(tmp)
    except Exception:
        pass

    if rc == 0 and out.strip():
        return out.strip(), latency
    return None, None


# ── API ──────────────────────────────────────────────────────────────────────


@app.route("/api/status")
def api_status():
    _, _, rc = run("systemctl is-active %s" % SERVICE)
    active = rc == 0
    cfg = read_config()
    srv = get_ss_server(cfg) if cfg else None
    out_ip, _, _ = run(
        "curl -s --max-time 6 --proxy http://127.0.0.1:1081 https://api.ipify.org",
        timeout=8,
    )
    return jsonify(
        {
            "active": active,
            "node": srv,
            "outbound_ip": out_ip.strip() if out_ip else None,
        }
    )


@app.route("/api/service/<action>", methods=["POST"])
def api_service(action):
    if action not in ("start", "stop", "restart"):
        return jsonify({"ok": False, "msg": "invalid action"}), 400
    _, err, rc = run("systemctl %s %s" % (action, SERVICE))
    return jsonify({"ok": rc == 0, "msg": err})


@app.route("/api/subscribe", methods=["POST"])
def api_subscribe():
    url = (request.json or {}).get("url", "").strip()
    if not url:
        return jsonify({"ok": False, "msg": "URL 为空"}), 400

    out, err, rc = run("curl -sL --max-time 15 '%s'" % url, timeout=20)
    if rc != 0:
        return jsonify({"ok": False, "msg": "下载失败: %s" % err}), 400

    raw = out.strip()

    # 过滤 # 注释行
    lines = [l for l in raw.splitlines() if not l.strip().startswith("#")]
    raw_clean = "\n".join(lines).strip()

    if raw_clean.startswith("["):
        nodes = parse_ssa_json(raw_clean)
        fmt = "SSA JSON"
    elif (
        "proxies:" in raw_clean
        or "type: ss" in raw_clean
        or "type: shadowsocks" in raw_clean
    ):
        nodes = parse_clash_yaml(raw_clean)
        fmt = "Clash YAML"
    else:
        nodes = parse_ssa_json(raw_clean)
        if not nodes:
            nodes = parse_clash_yaml(raw_clean)
        fmt = "auto"

    if not nodes:
        preview = raw_clean[:120].replace("\n", " ")
        return jsonify(
            {"ok": False, "msg": "未解析到节点，格式：%s，内容：%s" % (fmt, preview)}
        ), 400

    return jsonify({"ok": True, "count": len(nodes), "nodes": nodes, "fmt": fmt})


@app.route("/api/test", methods=["POST"])
def api_test():
    data = request.json or {}
    addr = data.get("server", "")
    port = int(data.get("port", 0))
    pwd = data.get("password", "")
    method = data.get("method", "chacha20-ietf-poly1305")

    real_addr = addr
    if addr and not re.match(r"^\d+\.\d+\.\d+\.\d+$", addr):
        ip = resolve_doh(addr)
        if ip:
            real_addr = ip

    out_ip, latency = test_node_connectivity(real_addr, port, pwd, method)
    return jsonify(
        {"ok": bool(out_ip), "ip": out_ip, "latency": latency, "real_addr": real_addr}
    )


@app.route("/api/apply", methods=["POST"])
def api_apply():
    data = request.json or {}
    addr = data.get("server", "")
    port = int(data.get("port", 0))
    pwd = data.get("password", "")
    method = data.get("method", "chacha20-ietf-poly1305")

    real_addr = addr
    if addr and not re.match(r"^\d+\.\d+\.\d+\.\d+$", addr):
        ip = resolve_doh(addr)
        if ip:
            real_addr = ip

    cfg = read_config()
    if not cfg:
        return jsonify({"ok": False, "msg": "读取配置文件失败"}), 500

    if not cfg.get("outbounds"):
        cfg["outbounds"] = []

    ss_outbound = None
    for ob in cfg["outbounds"]:
        if ob.get("protocol") == "shadowsocks":
            ss_outbound = ob
            break
    if ss_outbound is None:
        ss_outbound = {
            "protocol": "shadowsocks",
            "settings": {"servers": [{}]},
            "tag": "proxy",
        }
        cfg["outbounds"].insert(0, ss_outbound)

    ss_outbound["settings"]["servers"][0] = {
        "address": real_addr,
        "port": port,
        "method": method,
        "password": pwd,
    }

    write_config(cfg)
    _, err, rc = run("systemctl restart %s" % SERVICE)
    return jsonify({"ok": rc == 0, "msg": err, "real_addr": real_addr})


@app.route("/api/settings", methods=["GET", "POST"])
def api_settings():
    cfg = read_config()
    if not cfg:
        return jsonify({"ok": False, "msg": "读取配置失败"}), 500

    if request.method == "GET":
        socks_port = 1080
        http_port = 1081
        for ib in cfg.get("inbounds", []):
            if ib.get("protocol") == "socks":
                socks_port = ib.get("port", 1080)
            elif ib.get("protocol") == "http":
                http_port = ib.get("port", 1081)
        routing = "routing" in cfg
        return jsonify(
            {
                "ok": True,
                "socks_port": socks_port,
                "http_port": http_port,
                "routing": routing,
            }
        )

    data = request.json or {}
    socks_port = int(data.get("socks_port", 1080))
    http_port = int(data.get("http_port", 1081))
    routing = bool(data.get("routing", False))

    for ib in cfg.get("inbounds", []):
        if ib.get("protocol") == "socks":
            ib["port"] = socks_port
        elif ib.get("protocol") == "http":
            ib["port"] = http_port

    if routing:
        cfg["routing"] = {
            "domainStrategy": "IPIfNonMatch",
            "rules": [
                {"type": "field", "outboundTag": "direct", "domain": ["geosite:cn"]},
                {
                    "type": "field",
                    "outboundTag": "direct",
                    "ip": ["geoip:cn", "geoip:private"],
                },
            ],
        }
        tags = [o.get("tag") for o in cfg.get("outbounds", [])]
        if "direct" not in tags:
            cfg["outbounds"].append({"protocol": "freedom", "tag": "direct"})
    else:
        cfg.pop("routing", None)

    write_config(cfg)
    _, err, rc = run("systemctl restart %s" % SERVICE)
    return jsonify({"ok": rc == 0, "msg": err})


@app.route("/api/log")
def api_log():
    out, _, _ = run("journalctl -u %s -n 80 --no-pager --output=short" % SERVICE)
    return jsonify({"log": out})


@app.route("/")
def index():
    html = os.path.join(os.path.dirname(os.path.abspath(__file__)), "xray-panel.html")
    return send_file(html)


if __name__ == "__main__":
    print("=" * 50)
    print("  Xray 管理面板")
    print("  访问地址：http://0.0.0.0:8080")
    print("=" * 50)
    app.run(host="0.0.0.0", port=8080, debug=False)
