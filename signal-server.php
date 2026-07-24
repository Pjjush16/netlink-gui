<?php
/**
 * NetLink Signal Server v1.1
 * 
 * Centralized VIP allocation + node discovery
 * 
 * Deploy: upload to any PHP hosting (InfinityFree, etc.)
 * Ensure directory is writable for netlink_nodes.json
 */

$DATA_FILE = __DIR__ . '/netlink_nodes.json';
$EXPIRY_TIME = 300; // 5 minutes
$VIP_RANGE_START = 1;
$VIP_RANGE_END = 254;

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
    if (!file_exists($DATA_FILE)) {
        return [];
    }
    $content = file_get_contents($DATA_FILE);
    if ($content === false || $content === '') {
        return [];
    }
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

/**
 * Allocate a VIP that is not already in use by an online node.
 * If the node already has a VIP, return it (sticky allocation).
 */
function allocateVIP(&$nodes, $nodeId) {
    global $VIP_RANGE_START, $VIP_RANGE_END;
    
    // Check if this node already has a VIP
    if (isset($nodes[$nodeId]) && !empty($nodes[$nodeId]['virtual_ip'])) {
        return $nodes[$nodeId]['virtual_ip'];
    }
    
    // Collect all VIPs currently in use by online nodes
    $usedVIPs = [];
    foreach ($nodes as $n) {
        if (!empty($n['virtual_ip']) && !empty($n['online'])) {
            $usedVIPs[$n['virtual_ip']] = true;
        }
    }
    
    // Find the first available VIP
    for ($i = $VIP_RANGE_START; $i <= $VIP_RANGE_END; $i++) {
        $vip = "10.0.0.$i";
        if (!isset($usedVIPs[$vip])) {
            return $vip;
        }
    }
    
    return null; // All IPs exhausted
}

$action = isset($_GET['action']) ? $_GET['action'] : '';

switch ($action) {
    // Register node - server allocates VIP
    case 'register':
        $input = json_decode(file_get_contents('php://input'), true);
        
        if (!$input || !isset($input['node_id'])) {
            http_response_code(400);
            echo json_encode(['error' => 'Missing required field: node_id']);
            exit;
        }
        
        $nodes = loadNodes();
        cleanExpired($nodes);
        
        $nodeId = $input['node_id'];
        
        // Allocate VIP (sticky: same node gets same VIP)
        $vip = allocateVIP($nodes, $nodeId);
        if ($vip === null) {
            http_response_code(503);
            echo json_encode(['error' => 'No available virtual IPs']);
            exit;
        }
        
        $nodes[$nodeId] = [
            'node_id' => $nodeId,
            'virtual_ip' => $vip,
            'public_addr' => isset($input['public_addr']) ? $input['public_addr'] : '',
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
    
    // Heartbeat - update last_seen
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
            saveNodes($nodes);
            echo json_encode([
                'status' => 'ok',
                'virtual_ip' => $nodes[$nodeId]['virtual_ip']
            ]);
        } else {
            http_response_code(404);
            echo json_encode(['error' => 'Node not found, please register first']);
        }
        break;
    
    // Lookup node by virtual IP
    case 'lookup':
        $virtualIp = isset($_GET['virtual_ip']) ? $_GET['virtual_ip'] : '';
        
        if (empty($virtualIp)) {
            http_response_code(400);
            echo json_encode(['error' => 'Missing virtual_ip parameter']);
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
        echo json_encode(['error' => "Node not found: $virtualIp"]);
        break;
    
    // List all online nodes
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
    
    // Deregister node
    case 'deregister':
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
            $nodes[$nodeId]['online'] = false;
            saveNodes($nodes);
        }
        echo json_encode(['status' => 'ok', 'message' => 'Node deregistered']);
        break;
    
    // Server status
    case 'ping':
        $nodes = loadNodes();
        $onlineCount = 0;
        foreach ($nodes as $n) {
            if (!empty($n['online']) && (time() - $n['last_seen']) < 300) {
                $onlineCount++;
            }
        }
        echo json_encode([
            'server' => 'NetLink Signal Server v1.1',
            'features' => ['centralized_vip_allocation'],
            'nodes_online' => $onlineCount,
            'time' => time()
        ]);
        break;
    
    default:
        echo json_encode([
            'server' => 'NetLink Signal Server v1.1',
            'features' => ['centralized_vip_allocation'],
            'endpoints' => [
                'register'    => 'POST ?action=register {node_id, public_addr, mode} → {virtual_ip}',
                'heartbeat'   => 'POST ?action=heartbeat {node_id} → {virtual_ip}',
                'lookup'      => 'GET  ?action=lookup&virtual_ip=X.X.X.X → node info',
                'list'        => 'GET  ?action=list → [nodes]',
                'deregister'  => 'POST ?action=deregister {node_id}',
                'ping'        => 'GET  ?action=ping → server status'
            ]
        ]);
        break;
}
