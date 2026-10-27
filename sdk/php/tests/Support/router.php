<?php

declare(strict_types=1);

// Router of the PHP built-in server used by ServerTest: a tiny fake of a few Payrudder routes.

$state = (string) getenv('PAYRUDDER_TEST_STATE');
$method = $_SERVER['REQUEST_METHOD'];
$path = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);

header('Content-Type: application/json');
header('X-Request-Id: srv-1');

if (($_SERVER['HTTP_AUTHORIZATION'] ?? '') !== 'Bearer qk_test_server_secret') {
    http_response_code(401);
    echo '{"error":{"code":"UNAUTHENTICATED","key":"gateway.auth.invalid_key","message":"bad key"}}';

    return;
}

if ($method === 'GET' && $path === '/v1/customers') {
    $n = (int) @file_get_contents($state . '.flaky') + 1;
    file_put_contents($state . '.flaky', (string) $n);
    if ($n === 1) {
        http_response_code(429);
        header('Retry-After: 0');
        echo '{"error":{"code":"RESOURCE_EXHAUSTED","key":"gateway.rate_limited","message":"slow down"}}';

        return;
    }
    $token = $_GET['page_token'] ?? '';
    if ($token === '') {
        echo '{"customers":[{"external_id":"a"},{"external_id":"b"}],"next_page_token":"P2"}';
    } else {
        echo '{"customers":[{"external_id":"c"}],"next_page_token":""}';
    }

    return;
}
if ($method === 'POST' && $path === '/v1/customers') {
    $in = json_decode((string) file_get_contents('php://input'), true);
    http_response_code(201);
    echo json_encode(['external_id' => $in['external_id'], 'name' => $in['name'], 'ua' => $_SERVER['HTTP_USER_AGENT'] ?? '']);

    return;
}
if ($method === 'GET' && $path === '/v1/customers/slow') {
    usleep(800_000);
    echo '{}';

    return;
}
if ($method === 'GET' && $path === '/v1/customers/missing') {
    http_response_code(404);
    echo '{"error":{"code":"NOT_FOUND","key":"plans.customer.not_found","message":"no such customer","request_id":"req-404"}}';

    return;
}
if ($method === 'GET' && $path === '/v1/invoices/inv1') {
    echo '{"id":"inv1","total_minor":"1116","status":"paid"}';

    return;
}
if ($method === 'GET' && $path === '/v1/redirect') {
    header('Location: http://127.0.0.1:1/steal');
    http_response_code(302);

    return;
}
if ($method === 'POST' && $path === '/v1/assets') {
    // Started with enable_post_data_reading=0, so the raw multipart body is readable.
    $raw = (string) file_get_contents('php://input');
    preg_match_all('/name="file"; filename="([^"]*)"\r\nContent-Type: [^\r]*\r\n\r\n(.*?)\r\n--/s', $raw, $m, PREG_SET_ORDER);
    $names = [];
    foreach ($m as $i => $part) {
        $names[] = ['filename' => $part[1], 'status' => 'created', 'asset' => ['id' => 'a' . $i, 'size_bytes' => (string) strlen($part[2])]];
    }
    $meta = null;
    if (preg_match('/name="metadata"\r\nContent-Type: application\/json\r\n\r\n(.*?)\r\n--/s', $raw, $mm)) {
        $meta = $mm[1];
    }
    echo json_encode(['results' => $names, 'metadata' => $meta]);

    return;
}

http_response_code(404);
echo '{"error":{"code":"NOT_FOUND","key":"common.not_found","message":"no route"}}';
