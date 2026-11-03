<?php
// Looks the classes and methods of a Connect snippet up in the SDK's source. usage: php check.php <sdk/php directory> snippet.php...
// Run with -d zend.assertions=1 -d assert.exception=1, so a missing class or method is an uncaught AssertionError.
[$self, $sdk] = $argv;
spl_autoload_register(function (string $class) use ($sdk): void {
    if (str_starts_with($class, 'Payrudder\\')) {
        $file = $sdk . '/src/' . str_replace('\\', '/', substr($class, strlen('Payrudder\\'))) . '.php';
        if (is_file($file)) {
            require $file;
        }
    }
});
$client = new Payrudder\Client(apiKey: 'qk_test_x', baseUrl: 'http://localhost');
foreach (array_slice($argv, 2) as $path) {
    $code = file_get_contents($path);
    preg_match_all('/^use\s+([\w\\\\]+);/m', $code, $uses);
    $alias = [];
    foreach ($uses[1] as $fqcn) {
        $alias[substr(strrchr('\\' . $fqcn, '\\'), 1)] = $fqcn;
    }
    preg_match_all('/\\\\?(Payrudder(?:\\\\\w+)+)/', $code, $named);
    foreach (array_unique([...$uses[1], ...$named[1]]) as $class) {
        assert(class_exists($class) || interface_exists($class), "$class is not in the SDK");
    }
    preg_match_all('/\$client->(\w+)->(\w+)\(/', $code, $calls, PREG_SET_ORDER);
    foreach ($calls as [$all, $resource, $method]) {
        assert(method_exists($client->$resource, $method), "\$client->$resource->$method does not exist");
    }
    preg_match_all('/(?<![\w\\\\])([A-Z]\w*)::(\w+)\(/', $code, $statics, PREG_SET_ORDER);
    foreach ($statics as [$all, $class, $method]) {
        assert(isset($alias[$class]) && method_exists($alias[$class], $method), "$class::$method does not exist (is the class imported with use?)");
    }
}
echo "ok\n";
