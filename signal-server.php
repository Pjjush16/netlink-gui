<?php
/**
 * NetLink Signal Server v1.2
 * 
 * Features:
 * - Centralized VIP allocation
 * - Node discovery
 * - Data relay (when P2P fails)
 * 
 * Deploy: upload to any PHP hosting
 */

$DATA_FILE = __DIR__ . '/netlink_nodes.json';
$RELAY_DIR = __DIR__ . '/netlink_relay';
$EXPIRY_TIME = 300;
$VIP_RANGE_START = 1;
$VIP_RANGE_END = 254;

// Ensure relay directory exists
if (!is_dir($RELAY_DIR)) {
    @mkdir($RELAY_DIR, 0777, true);
}

header('Content-Type: application/json; charset=utf-8');
header('Access-Control-Allow-Origin: *');
header('Access-Control-Allow-Methods: GET, POST, OPTIONS');
header('Access-Control-Allow-Headers: Content-Type, X-Node-ID');

if ($_SERVER['REQUEST_METHOD'] === 'OPTIONS') {
    http_response_code(200);
    exit;
}

function loadNodes() {
    global $DATA_FILE;
    if (!file_exists($DATA_FILE)) return [];
    $content = file_get_contents($DATA_FILE);
    if ($content === false || $content === '') return [];
    $nodes = json_decode($content, true);
    return is_array($nodes) ? $nodes : [];
}

function saveNodes($nodes) {
    global $DATA_FILE;
    file_put_contents($DATA_FILE, json_encode($nodes, JSON_PRETTY_PRINT), LOCK_EX);
}

function cleanExpired(&$nodes) {
    global $EXPIRY_TIME;
    $now = time();
    foreach ($nodes as $nodeId => $node) {
        if (isset($node['last_seen']) && ($now - $node['last_seen']) > $EXPIRY_TIME) {
            unset($nodes[$nodeId]);
        }
    }
}

function allocateVIP(&$nodes, $nodeId) {
    global $VIP_RANGE_START, $VIP_RANGE_END;
    
    if (isset($nodes[$nodeId]) && !empty($nodes[$nodeId]['virtual_ip'])) {
        return $nodes[$nodeId]['virtual_ip'];
    }
    
    $usedVIPs = [];
    foreach ($nodes as $n) {
        if (!empty($n['virtual_ip']) && !empty($n['online'])) {
            $usedVIPs[$n['virtual_ip']] = true;
        }
    }
    
    for ($i = $VIP_RANGE_START; $i <= $VIP_RANGE_END; $i++) {
        $vip = "10.0.0.$i";
        if (!isset($usedVIPs[$vip])) {
            return $vip;
        }
    }
    return null;
}

// Relay: get the real client IP (handles Cloudflare/proxy)
function getClientIP() {
    if (isset($_SERVER['HTTP_CF_CONNECTING_IP'])) {
        return $_SERVER['HTTP_CF_CONNECTING_IP'];
    }
    if (isset($_SERVER['HTTP_X_FORWARDED_FOR'])) {
        $ips = explode(',', $_SERVER['HTTP_X_FORWARDED_FOR']);
        return trim($ips[0]);
    }
    if (isset($_SERVER['HTTP_X_REAL_IP'])) {
        return $_SERVER['HTTP_X_REAL_IP'];
    }
    return $_SERVER['REMOTE_ADDR'] ?? '0.0.0.0';
}

function getClientPort() {
    if (isset($_SERVER['HTTP_CF_CONNECTING_PORT'])) {
        return $_SERVER['HTTP_CF_CONNECTING_PORT'];
    }
    return $_SERVER['REMOTE_PORT'] ?? '0';
}

function cleanOldRelayMessages() {
    global $RELAY_DIR;
    $files = glob($RELAY_DIR . '/*.msg');
    $now = time();
    foreach ($files as $f) {
        if ($now - filemtime($f) > 30) { // 30 second expiry for relay messages
            @unlink($f);
        }
    }
}

$action = isset($_GET['action']) ? $_GET['action'] : '';

switch ($action) {
    case 'register':
        $input = json_decode(file_get_contents('php://input'), true);
        
        if (!$input || !isset($input['node_id'])) {
            http_response_code(400);
            echo json_encode(['error' => 'Missing node_id']);
            exit;
        }
        
        $nodes = loadNodes();
        cleanExpired($nodes);
        
        $nodeId = $input['node_id'];
        $vip = allocateVIP($nodes, $nodeId);
        if ($vip === null) {
            http_response_code(503);
            echo json_encode(['error' => 'No available VIPs']);
            exit;
        }
        
        // Use client's real IP from Cloudflare headers if available
        $clientIP = getClientIP();
        $clientPort = getClientPort();
        $realAddr = $clientIP . ':' . $clientPort;
        
        // If node provided its own public_addr (from STUN), prefer that
        $publicAddr = isset($input['public_addr']) ? $input['public_addr'] : $realAddr;
        
        $nodes[$nodeId] = [
            'node_id' => $nodeId,
            'virtual_ip' => $vip,
            'public_addr' => $publicAddr,
            'real_addr' => $realAddr,
            'mode' => isset($input['mode']) ? $input['mode'] : 'udp',
            'online' => true,
            'last_seen' => time()
        ];
        
        saveNodes($nodes);
        echo json_encode([
            'status' => 'ok',
            'virtual_ip' => $vip,
            'message' => "Registered with VIP $vip"
        ]);
        break;
    
    case 'heartbeat':
        $input = json_decode(file_get_contents('php://input'), true);
        $nodeId = isset($input['node_id']) ? $input['node_id'] : '';
        
        if (empty($nodeId)) {
            $nodeId = isset($_SERVER['HTTP_X_NODE_ID']) ? $_SERVER['HTTP_X_NODE_ID'] : '';
        }
        
        if (empty($nodeId)) {
            http_response_code(400);
            echo json_encode(['error' => 'Missing node_id']);
            exit;
        }
        
        $nodes = loadNodes();
        
        if (isset($nodes[$nodeId])) {
            $nodes[$nodeId]['last_seen'] = time();
            $nodes[$nodeId]['online'] = true;
            // Update real_addr on each heartbeat
            $nodes[$nodeId]['real_addr'] = getClientIP() . ':' . getClientPort();
            saveNodes($nodes);
            echo json_encode([
                'status' => 'ok',
                'virtual_ip' => $nodes[$nodeId]['virtual_ip']
            ]);
        } else {
            http_response_code(404);
            echo json_encode(['error' => 'Node not found']);
        }
        break;
    
    case 'lookup':
        $virtualIp = isset($_GET['virtual_ip']) ? $_GET['virtual_ip'] : '';
        
        if (empty($virtualIp)) {
            http_response_code(400);
            echo json_encode(['error' => 'Missing virtual_ip']);
            exit;
        }
        
        $nodes = loadNodes();
        cleanExpired($nodes);
        saveNodes($nodes);
        
        foreach ($nodes as $node) {
            if ($node['virtual_ip'] === $virtualIp && !empty($node['online'])) {
                echo json_encode($node);
                exit;
            }
        }
        
        http_response_code(404);
        echo json_encode(['error' => "Not found: $virtualIp"]);
        break;
    
    case 'list':
        $nodes = loadNodes();
        cleanExpired($nodes);
        saveNodes($nodes);
        
        $onlineNodes = [];
        foreach ($nodes as $node) {
            if (!empty($node['online'])) {
                $onlineNodes[] = $node;
            }
        }
        
        echo json_encode($onlineNodes);
        break;
    
    case 'deregister':
        $input = json_decode(file_get_contents('php://input'), true);
        $nodeId = isset($input['node_id']) ? $input['node_id'] : '';
        
        if (empty($nodeId)) {
            $nodeId = isset($_SERVER['HTTP_X_NODE_ID']) ? $_SERVER['HTTP_X_NODE_ID'] : '';
        }
        
        $nodes = loadNodes();
        if (isset($nodes[$nodeId])) {
            $nodes[$nodeId]['online'] = false;
            saveNodes($nodes);
        }
        echo json_encode(['status' => 'ok']);
        break;
    
    // ===== RELAY ENDPOINTS =====
    
    // Send data to a peer via relay
    case 'relay':
        $input = json_decode(file_get_contents('php://input'), true);
        
        if (!$input || !isset($input['to']) || !isset($input['from']) || !isset($input['data'])) {
            http_response_code(400);
            echo json_encode(['error' => 'Missing to/from/data fields']);
            exit;
        }
        
        cleanOldRelayMessages();
        
        $to = preg_replace('/[^a-zA-Z0-9._-]/', '', $input['to']);
        $from = preg_replace('/[^a-zA-Z0-9._-]/', '', $input['from']);
        $data = $input['data']; // base64 encoded
        
        // Store message for recipient to pick up
        $msgFile = $RELAY_DIR . '/' . $to . '_' . time() . '_' . mt_rand(1000, 9999) . '.msg';
        $msg = json_encode([
            'from' => $from,
            'data' => $data,
            'time' => time()
        ]);
        
        file_put_contents($msgFile, $msg, LOCK_EX);
        
        echo json_encode(['status' => 'ok', 'relay' => true]);
        break;
    
    // Poll for incoming relay messages
    case 'relay_poll':
        $nodeId = isset($_GET['node_id']) ? $_GET['node_id'] : '';
        
        if (empty($nodeId)) {
            http_response_code(400);
            echo json_encode(['error' => 'Missing node_id']);
            exit;
        }
        
        cleanOldRelayMessages();
        
        $safeId = preg_replace('/[^a-zA-Z0-9._-]/', '', $nodeId);
        $pattern = $RELAY_DIR . '/' . $safeId . '_*.msg';
        $files = glob($pattern);
        
        $messages = [];
        foreach ($files as $f) {
            $content = file_get_contents($f);
            if ($content !== false) {
                $msg = json_decode($content, true);
                if ($msg) {
                    $messages[] = $msg;
                }
            }
            @unlink($f); // Delete after reading
        }
        
        echo json_encode([
            'status' => 'ok',
            'messages' => $messages,
            'count' => count($messages)
        ]);
        break;
    
    case 'ping':
        $nodes = loadNodes();
        $onlineCount = 0;
        foreach ($nodes as $n) {
            if (!empty($n['online']) && (time() - $n['last_seen']) < 300) {
                $onlineCount++;
            }
        }
        echo json_encode([
            'server' => 'NetLink Signal Server v1.2',
            'features' => ['centralized_vip_allocation', 'data_relay'],
            'nodes_online' => $onlineCount,
            'time' => time()
        ]);
        break;
    
    default:
        echo json_encode([
            'server' => 'NetLink Signal Server v1.2',
            'features' => ['centralized_vip_allocation', 'data_relay'],
            'endpoints' => [
                'register'    => 'POST ?action=register {node_id, public_addr, mode}',
                'heartbeat'   => 'POST ?action=heartbeat {node_id}',
                'lookup'      => 'GET  ?action=lookup&virtual_ip=X.X.X.X',
                'list'        => 'GET  ?action=list',
                'deregister'  => 'POST ?action=deregister {node_id}',
                'relay'       => 'POST ?action=relay {from, to, data}',
                'relay_poll'  => 'GET  ?action=relay_poll&node_id=X',
                'ping'        => 'GET  ?action=ping'
            ]
        ]);
        break;
}
