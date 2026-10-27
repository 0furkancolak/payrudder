<?php

declare(strict_types=1);

/**
 * Generates the typed models, enums and resource classes from api/openapi/v1.yaml.
 *
 *   composer generate
 *
 * Only the tenant API (`/v1/*`) is covered. The hand-written parts of the SDK
 * (client, transport, retries, webhooks, ingest and upload helpers) live next to the generated
 * code and are never touched by this script.
 */

use Payrudder\Tools\SpecLoader;

require __DIR__ . '/../vendor/autoload.php';

const SKIP_OPERATIONS = ['uploadAssets']; // multipart/form-data: hand-written in Resource/AssetsUploads.php
const RESOURCE_TRAITS = [
    'events' => 'EventsIngest',
    'assets' => 'AssetsUploads',
];

final class Gen
{
    /** @var array<string,mixed> */
    private array $spec;

    /** @var array<string,array{name:string,props:array<string,array<string,mixed>>,required:list<string>,request:bool,response:bool,desc:string}> */
    private array $models = [];

    /** @var array<string,list<string>> enum class => values */
    private array $enums = [];

    /** @var array<string,string> */
    private array $warnings = [];

    public function __construct(private readonly string $root)
    {
        $this->spec = SpecLoader::load();
    }

    public function run(): void
    {
        $ops = $this->operations();
        $resources = [];
        foreach ($ops as &$op) {
            $this->collect($op);
        }
        unset($op);
        foreach ($ops as $op) {
            $resources[$op['resource']][] = $op;
        }

        $this->cleanDir($this->root . '/src/Model');
        $this->cleanDir($this->root . '/src/Enum');
        $this->cleanDir($this->root . '/src/Resource/Generated');

        foreach ($this->models as $m) {
            $this->write('src/Model/' . $m['name'] . '.php', $this->renderModel($m));
        }
        foreach ($this->enums as $name => $values) {
            $this->write('src/Enum/' . $name . '.php', $this->renderEnum($name, $values));
        }
        ksort($resources);
        foreach ($resources as $res => $list) {
            $this->write('src/Resource/Generated/' . $this->resourceClass($res) . '.php', $this->renderResource($res, $list));
        }
        $this->write('src/Resource/Generated/ResourceHolder.php', $this->renderResources(array_keys($resources)));

        foreach ($this->warnings as $k => $w) {
            fwrite(STDERR, "warning: $k: $w\n");
        }
        fwrite(STDOUT, sprintf("%d operations, %d models, %d enums, %d resources\n", count($ops), count($this->models), count($this->enums), count($resources)));
    }

    // ---------------------------------------------------------------- spec walking

    /** @return list<array<string,mixed>> */
    private function operations(): array
    {
        $out = [];
        /** @var array<string,array<string,mixed>> $paths */
        $paths = $this->spec['paths'];
        foreach ($paths as $path => $item) {
            if (!str_starts_with($path, '/v1/')) {
                continue;
            }
            foreach (['get', 'post', 'put', 'patch', 'delete'] as $method) {
                if (!isset($item[$method])) {
                    continue;
                }
                /** @var array<string,mixed> $op */
                $op = $item[$method];
                $params = array_merge($item['parameters'] ?? [], $op['parameters'] ?? []);
                $parsed = [];
                foreach ($params as $p) {
                    $p = $this->resolve($p);
                    $parsed[] = $p;
                }
                $segments = explode('/', trim($path, '/'));
                $out[] = [
                    'id' => $op['operationId'],
                    'method' => strtoupper($method),
                    'path' => $path,
                    'resource' => $segments[1],
                    'summary' => $op['summary'] ?? '',
                    'params' => $parsed,
                    'body' => $op['requestBody'] ?? null,
                    'responses' => $op['responses'],
                ];
            }
        }

        return $out;
    }

    /**
     * @param array<string,mixed> $node
     *
     * @return array<string,mixed>
     */
    private function resolve(array $node): array
    {
        while (isset($node['$ref'])) {
            $node = $this->lookup((string) $node['$ref']);
        }

        return $node;
    }

    /** @return array<string,mixed> */
    private function lookup(string $ref): array
    {
        $parts = explode('/', ltrim($ref, '#/'));
        $cur = $this->spec;
        foreach ($parts as $p) {
            $cur = $cur[$p] ?? throw new RuntimeException("unresolved $ref");
        }

        return $cur;
    }

    // ---------------------------------------------------------------- collection of models

    /** @param array<string,mixed> $op */
    private function collect(array &$op): void
    {
        $base = $this->pascal($op['id']);
        $op['bodyType'] = null;
        $op['multipart'] = false;
        if ($op['body'] !== null) {
            $body = $this->resolve($op['body']);
            $content = $body['content'] ?? [];
            $ct = array_key_first($content);
            if ($ct !== 'application/json') {
                $op['multipart'] = true;
            } else {
                $op['bodyRequired'] = (bool) ($body['required'] ?? false);
                $op['bodyType'] = $this->typeOf($content[$ct]['schema'], $base . 'Request', true);
            }
        }
        $op['returns'] = ['kind' => 'void'];
        foreach ($op['responses'] as $code => $resp) {
            if (!str_starts_with((string) $code, '2')) {
                continue;
            }
            $resp = $this->resolve($resp);
            $content = $resp['content'] ?? null;
            if ($content === null) {
                $op['returns'] = ['kind' => 'void'];
                break;
            }
            $ct = array_key_first($content);
            if ($ct !== 'application/json') {
                $op['returns'] = ['kind' => 'raw'];
                break;
            }
            $t = $this->typeOf($content[$ct]['schema'], $base . 'Response', false);
            $op['returns'] = ['kind' => 'json', 'type' => $t];
            break;
        }
    }

    /**
     * Describes a schema as a PHP type and registers the models and enums it needs.
     *
     * @param array<string,mixed> $schema
     *
     * @return array<string,mixed>
     */
    private function typeOf(array $schema, string $ctx, bool $request, ?string $propName = null, bool $named = false): array
    {
        $refName = null;
        if (isset($schema['$ref'])) {
            $refName = basename((string) $schema['$ref']);
            if (str_starts_with((string) $schema['$ref'], '#/components/schemas/')) {
                $schema = $this->lookup((string) $schema['$ref']);
                $ctx = $refName;
                $named = true;
            } else {
                $schema = $this->resolve($schema);
            }
        }
        if (isset($schema['allOf']) && count($schema['allOf']) === 1) {
            return $this->typeOf($schema['allOf'][0], $ctx, $request, $propName);
        }
        if (isset($schema['allOf'])) {
            // Merge the parts into one object schema.
            $merged = ['type' => 'object', 'properties' => [], 'required' => []];
            foreach ($schema['allOf'] as $part) {
                $part = $this->resolve($part);
                $merged['properties'] = array_merge($merged['properties'], $part['properties'] ?? []);
                $merged['required'] = array_merge($merged['required'], $part['required'] ?? []);
                if (isset($part['description']) && !isset($merged['description'])) {
                    $merged['description'] = $part['description'];
                }
            }
            $schema = $merged;
        }
        if (isset($schema['oneOf']) || isset($schema['anyOf'])) {
            $alts = $schema['oneOf'] ?? $schema['anyOf'];
            $scalars = [];
            foreach ($alts as $alt) {
                $t = $alt['type'] ?? null;
                if (!in_array($t, ['string', 'integer', 'number'], true) || isset($alt['enum'])) {
                    $this->warnings[$ctx . ($propName ? ".$propName" : '')] = 'oneOf/anyOf/allOf with non-scalar alternatives is typed as a free-form value';

                    return ['kind' => 'mixed'];
                }
                $scalars[$t === 'integer' ? 'int' : ($t === 'number' ? 'float' : 'string')] = true;
            }

            if (isset($scalars['float'])) {
                $scalars['int'] = true;
            }

            return ['kind' => 'union', 'types' => array_keys($scalars)];
        }

        $type = $schema['type'] ?? null;
        if (is_array($type)) {
            $type = array_values(array_filter($type, static fn($t) => $t !== 'null'))[0] ?? null;
        }
        if ($type === null && isset($schema['properties'])) {
            $type = 'object';
        }

        if (isset($schema['enum']) && $type === 'string') {
            $name = $named ? $ctx : $this->pascal($ctx) . ($propName ? $this->pascal($propName) : '');
            $name = $this->unique($name, array_map('strval', $schema['enum']));
            $this->enums[$name] = array_map('strval', $schema['enum']);

            return ['kind' => 'enum', 'class' => $name];
        }

        switch ($type) {
            case 'string':
                $isInt = ($schema['format'] ?? '') === 'int64'
                    || ($propName !== null && str_ends_with($propName, '_minor'))
                    || (isset($schema['description']) && preg_match('/\bint64\b/i', (string) $schema['description']) === 1);

                return ['kind' => $isInt ? 'int64s' : 'string'];
            case 'integer':
                return ['kind' => 'int'];
            case 'number':
                return ['kind' => 'float'];
            case 'boolean':
                return ['kind' => 'bool'];
            case 'array':
                $items = $schema['items'] ?? [];
                $it = $this->typeOf($items, $ctx . ($propName ? $this->pascal($propName) : '') . 'Item', $request, null);

                return ['kind' => 'list', 'item' => $it];
            case 'object':
                if (isset($schema['properties'])) {
                    $name = $named ? $ctx : $this->pascal($ctx) . ($propName ? $this->pascal($propName) : '');
                    $this->registerModel($name, $schema, $request);

                    return ['kind' => 'model', 'class' => $name];
                }
                if (isset($schema['additionalProperties']) && is_array($schema['additionalProperties'])) {
                    $v = $this->typeOf($schema['additionalProperties'], $ctx, $request, $propName);
                    if (in_array($v['kind'], ['string', 'int', 'int64s'], true)) {
                        return ['kind' => 'map', 'value' => $v['kind'] === 'string' ? 'string' : 'int'];
                    }
                }

                return ['kind' => 'map', 'value' => 'mixed'];
            default:
                $this->warnings[$ctx . ($propName ? ".$propName" : '')] = 'schema without a type is typed as a free-form value';

                return ['kind' => 'mixed'];
        }
    }

    /** @param list<string> $values */
    private function unique(string $name, array $values): string
    {
        $name = $this->pascal($name);
        if (isset($this->enums[$name]) && $this->enums[$name] !== $values) {
            $i = 2;
            while (isset($this->enums[$name . $i])) {
                $i++;
            }

            return $name . $i;
        }

        return $name;
    }

    /** @param array<string,mixed> $schema */
    private function registerModel(string $name, array $schema, bool $request): void
    {
        $name = $this->pascal($name);
        $reserved = ['Error', 'Exception', 'Model', 'Object', 'String', 'Int', 'Float', 'Bool', 'Mixed', 'Null', 'List'];
        if (in_array($name, $reserved, true)) {
            throw new RuntimeException("model name $name is reserved");
        }
        $first = !isset($this->models[$name]);
        if (!$first) {
            $m = &$this->models[$name];
            if ($request && !$m['request'] || !$request && !$m['response']) {
                $m[$request ? 'request' : 'response'] = true;
                $this->models[$name]['props'] = $this->buildProps($name, $schema, $request);
            }

            return;
        }
        $this->models[$name] = ['name' => $name, 'props' => [], 'required' => array_values($schema['required'] ?? []), 'request' => $request, 'response' => !$request, 'desc' => (string) ($schema['description'] ?? '')];
        $this->models[$name]['props'] = $this->buildProps($name, $schema, $request);
    }

    /**
     * @param array<string,mixed> $schema
     *
     * @return array<string,array<string,mixed>>
     */
    private function buildProps(string $name, array $schema, bool $request): array
    {
        $props = [];
        foreach ($schema['properties'] as $pname => $pschema) {
            $props[(string) $pname] = $this->typeOf($pschema, $name, $request, (string) $pname);
        }

        return $props;
    }

    // ---------------------------------------------------------------- rendering helpers

    private function pascal(string $s): string
    {
        $parts = preg_split('/[^A-Za-z0-9]+/', $s, -1, PREG_SPLIT_NO_EMPTY) ?: [];
        $out = '';
        foreach ($parts as $p) {
            $out .= ucfirst($p);
        }
        if ($out === '' || ctype_digit($out[0])) {
            $out = 'V' . $out;
        }

        return $out;
    }

    /** Case names: `AGGREGATION_SUM` -> `AggregationSum`, `invoice.paid` -> `InvoicePaid`. */
    private function casePascal(string $s): string
    {
        $parts = preg_split('/[^A-Za-z0-9]+/', $s, -1, PREG_SPLIT_NO_EMPTY) ?: [];
        $out = '';
        foreach ($parts as $p) {
            $out .= ucfirst(strtoupper($p) === $p ? strtolower($p) : $p);
        }
        if ($out === '' || ctype_digit($out[0])) {
            $out = 'V' . $out;
        }

        return $out;
    }

    private function camel(string $s): string
    {
        return lcfirst($this->pascal($s));
    }

    /** @param array<string,mixed> $t */
    private function native(array $t): string
    {
        return match ($t['kind']) {
            'string' => 'string',
            'int', 'int64s' => 'int',
            'float' => 'float',
            'bool' => 'bool',
            'enum' => '\\Payrudder\\Enum\\' . $t['class'] . '|string',
            'model' => '\\Payrudder\\Model\\' . $t['class'],
            'list', 'map' => 'array',
            'union' => implode('|', $t['types']),
            'mixed' => 'mixed',
            default => throw new RuntimeException('native ' . $t['kind']),
        };
    }

    /** @param array<string,mixed> $t */
    private function doc(array $t): string
    {
        return match ($t['kind']) {
            'list' => 'list<' . $this->doc($t['item']) . '>',
            'map' => 'array<string,' . $t['value'] . '>',
            'enum' => '\\Payrudder\\Enum\\' . $t['class'] . '|string',
            default => $this->native($t),
        };
    }

    /** @param array<string,mixed> $t */
    private function from(array $t, string $e): string
    {
        return match ($t['kind']) {
            'string' => "Cast::string($e)",
            'int', 'int64s' => "Cast::int($e)",
            'float' => "Cast::float($e)",
            'bool' => "Cast::bool($e)",
            'enum' => 'Cast::enum(\\Payrudder\\Enum\\' . $t['class'] . "::class, $e)",
            'model' => 'Cast::model(\\Payrudder\\Model\\' . $t['class'] . "::class, $e)",
            'union' => "Cast::scalar($e)",
            'mixed' => $e,
            'map' => match ($t['value']) {
                'string' => "Cast::stringMap($e)",
                'int' => "Cast::intMap($e)",
                default => "Cast::map($e)",
            },
            'list' => match ($t['item']['kind']) {
                'model' => 'Cast::models(\\Payrudder\\Model\\' . $t['item']['class'] . "::class, $e)",
                'enum' => 'Cast::enums(\\Payrudder\\Enum\\' . $t['item']['class'] . "::class, $e)",
                'string' => "Cast::strings($e)",
                'int', 'int64s' => "Cast::ints($e)",
                default => "Cast::list($e)",
            },
            default => throw new RuntimeException('from ' . $t['kind']),
        };
    }

    /** @param array<string,mixed> $t */
    private function wire(array $t, string $e): string
    {
        return match ($t['kind']) {
            'int64s' => "Cast::int64($e)",
            'list' => $t['item']['kind'] === 'int64s' ? "Cast::int64List($e)" : "Cast::wire($e)",
            'map' => "Cast::wireMap($e)",
            'string', 'int', 'float', 'bool', 'union', 'mixed' => $e,
            default => "Cast::wire($e)",
        };
    }

    private function docEscape(string $s): string
    {
        return trim(str_replace(['*/', "\n"], ['* /', ' '], $s));
    }

    private function header(): string
    {
        return "<?php\n\ndeclare(strict_types=1);\n\n// Generated by tools/generate.php from api/openapi/v1.yaml. Do not edit.\n\n";
    }

    // ---------------------------------------------------------------- models

    /** @param array<string,mixed> $m */
    private function renderModel(array $m): string
    {
        $strict = $m['request'] && !$m['response'];
        $props = $m['props'];
        $required = $strict ? $m['required'] : [];
        $names = array_keys($props);
        usort($names, static function (string $a, string $b) use ($required, $names): int {
            $ra = in_array($a, $required, true) ? 0 : 1;
            $rb = in_array($b, $required, true) ? 0 : 1;

            return $ra <=> $rb ?: array_search($a, $names, true) <=> array_search($b, $names, true);
        });

        $doc = [];
        $ctor = [];
        $from = [];
        $to = [];
        foreach ($names as $n) {
            $t = $props[$n];
            $camel = $this->camel($n);
            $isReq = in_array($n, $required, true);
            $native = $this->native($t);
            $nullable = !$isReq && $t['kind'] !== 'mixed';
            $decl = $nullable ? (str_contains($native, '|') ? $native . '|null' : '?' . $native) : $native;
            $docType = $this->doc($t) . ($nullable ? '|null' : '');
            $doc[] = "     * @param $docType \$$camel";
            $default = $isReq ? '' : ' = null';
            $ctor[] = "        public readonly $decl \$$camel$default,";
            $from[] = "            $camel: " . $this->from($t, "\$data['$n'] ?? null") . ',';
            $to[] = "            '$n' => " . $this->wire($t, "\$this->$camel") . ',';
        }

        $out = $this->header();
        $out .= "namespace Payrudder\\Model;\n\nuse Payrudder\\Contract\\" . ($strict ? 'RequestBody' : 'Model') . ";\nuse Payrudder\\Internal\\Cast;\n\n";
        $d = $this->docEscape($m['desc']);
        $out .= "/**\n * " . ($d !== '' ? $d : $m['name']) . "\n */\n";
        $out .= 'final class ' . $m['name'] . ' implements ' . ($strict ? 'RequestBody' : 'Model') . "\n{\n";
        $out .= "    /**\n" . implode("\n", $doc) . "\n     */\n";
        $out .= "    public function __construct(\n" . implode("\n", $ctor) . "\n    ) {\n    }\n";
        if (!$strict) {
            $out .= "\n    /**\n     * @param array<string,mixed> \$data\n     */\n";
            $out .= "    public static function fromArray(array \$data): static\n    {\n";
            $out .= "        return new static(\n" . implode("\n", $from) . "\n        );\n    }\n";
        }
        $out .= "\n    /**\n     * @return array<string,mixed>\n     */\n";
        $out .= "    public function toArray(): array\n    {\n";
        $out .= "        return Cast::compact([\n" . implode("\n", $to) . "\n        ]);\n    }\n}\n";

        return $out;
    }

    /** @param list<string> $values */
    private function renderEnum(string $name, array $values): string
    {
        $out = $this->header() . "namespace Payrudder\\Enum;\n\n";
        $out .= "enum $name: string\n{\n";
        $seen = [];
        foreach ($values as $v) {
            $case = $this->casePascal($v);
            while (isset($seen[$case])) {
                $case .= '_';
            }
            $seen[$case] = true;
            $out .= "    case $case = '" . addslashes($v) . "';\n";
        }
        $out .= "}\n";

        return $out;
    }

    // ---------------------------------------------------------------- resources

    private function resourceClass(string $segment): string
    {
        return $this->pascal($segment) . 'Resource';
    }

    /** @param list<string> $segments */
    private function renderResources(array $segments): string
    {
        $out = $this->header() . "namespace Payrudder\\Resource\\Generated;\n\nuse Payrudder\\Internal\\Requester;\n\n";
        $out .= "/**\n * The resource namespaces of the client, one per first path segment of the API.\n */\nabstract class ResourceHolder\n{\n";
        foreach ($segments as $s) {
            $out .= '    public readonly ' . $this->resourceClass($s) . ' $' . $this->camel($s) . ";\n";
        }
        $out .= "\n    public function __construct(Requester \$requester)\n    {\n";
        foreach ($segments as $s) {
            $out .= '        $this->' . $this->camel($s) . ' = new ' . $this->resourceClass($s) . "(\$requester);\n";
        }
        $out .= "    }\n}\n";

        return $out;
    }

    /** @param list<array<string,mixed>> $ops */
    private function renderResource(string $segment, array $ops): string
    {
        $cls = $this->resourceClass($segment);
        $trait = RESOURCE_TRAITS[$segment] ?? null;
        $out = $this->header() . "namespace Payrudder\\Resource\\Generated;\n\n";
        $out .= "use Payrudder\\Internal\\AbstractResource;\nuse Payrudder\\Internal\\Cast;\nuse Payrudder\\RequestOptions;\n";
        if ($trait !== null) {
            $out .= "use Payrudder\\Resource\\$trait;\n";
        }
        $out .= "\n/**\n * Operations under /v1/$segment.\n */\nfinal class $cls extends AbstractResource\n{\n";
        if ($trait !== null) {
            $out .= "    use $trait;\n\n";
        }
        $methods = [];
        foreach ($ops as $op) {
            if (in_array($op['id'], SKIP_OPERATIONS, true)) {
                continue;
            }
            $methods[] = $this->renderOperation($op);
        }
        $out .= implode("\n", $methods) . "}\n";

        return $out;
    }

    /** @param array<string,mixed> $op */
    private function renderOperation(array $op): string
    {
        $req = [];   // [decl, docline]
        $opt = [];
        $queryMap = [];
        $pathExpr = "'" . $op['path'] . "'";
        $callArgsForward = [];

        // path parameters, in path order
        preg_match_all('/\{([^}]+)\}/', $op['path'], $mm);
        $paramsByName = [];
        foreach ($op['params'] as $p) {
            $paramsByName[$p['in'] . ':' . $p['name']] = $p;
        }
        $pathVars = [];
        foreach ($mm[1] as $pn) {
            $var = $this->camel($pn);
            $req[] = ["string \$$var", "     * @param string \$$var"];
            $callArgsForward[] = "$var: \$$var";
            $pathVars[$pn] = $var;
        }
        $pathExpr = "'" . preg_replace_callback('/\{([^}]+)\}/', static fn($m) => "' . rawurlencode(\$" . lcfirst(str_replace(' ', '', ucwords(str_replace('_', ' ', $m[1])))) . ") . '", $op['path']) . "'";
        $pathExpr = str_replace(" . ''", '', $pathExpr);

        $hasBodyRequired = $op['bodyType'] !== null && ($op['bodyRequired'] ?? false);

        foreach ($op['params'] as $p) {
            if ($p['in'] !== 'query') {
                continue;
            }
            $schema = $p['schema'] ?? ['type' => 'string'];
            $t = $schema['type'] ?? 'string';
            if (is_array($t)) {
                $t = $t[0];
            }
            $native = match ($t) {
                'integer' => 'int',
                'boolean' => 'bool',
                'number' => 'float',
                default => 'string',
            };
            $var = $this->camel($p['name']);
            $callArgsForward[] = "$var: \$$var";
            $queryMap[] = "'" . $p['name'] . "' => \$$var";
            if (!empty($p['required'])) {
                $req[] = ["$native \$$var", "     * @param $native \$$var"];
            } else {
                $opt[] = ["?$native \$$var = null", "     * @param $native|null \$$var"];
            }
        }

        $bodyArg = 'null';
        if ($op['bodyType'] !== null) {
            $bt = $op['bodyType'];
            if ($bt['kind'] === 'model') {
                $cls = '\\Payrudder\\Model\\' . $bt['class'];
                $decl = "$cls|array \$body";
                $dl = "     * @param $cls|array<string,mixed> \$body";
            } else {
                $decl = 'array $body';
                $dl = '     * @param array<string,mixed> $body';
            }
            $callArgsForward[] = 'body: $body';
            $bodyArg = 'Cast::body($body)';
            if ($hasBodyRequired) {
                $req[] = [$decl, $dl];
            } else {
                $opt[] = [str_replace(' $body', '|null $body', $decl) . ' = null', str_replace(' $body', '|null $body', $dl)];
                $bodyArg = '$body === null ? null : Cast::body($body)';
            }
        }

        $params = array_merge($req, $opt, [['?RequestOptions $options = null', '     * @param RequestOptions|null $options']]);
        $callArgsForward[] = 'options: $options';

        $ret = $op['returns'];
        $retDecl = 'void';
        $retDoc = null;
        $callName = 'callVoid';
        if ($ret['kind'] === 'raw') {
            $retDecl = 'string';
            $callName = 'callRaw';
        } elseif ($ret['kind'] === 'json') {
            $t = $ret['type'];
            if ($t['kind'] === 'model') {
                $retDecl = '\\Payrudder\\Model\\' . $t['class'];
            } else {
                $retDecl = 'array';
                $retDoc = 'array<string,mixed>';
            }
            $callName = 'call';
        }

        $docLines = [];
        $summary = $this->docEscape($op['summary']);
        $docLines[] = '     * ' . ($summary !== '' ? $summary : $op['id']) . ' (' . $op['method'] . ' ' . $op['path'] . ').';
        $docLines[] = '     *';
        foreach ($params as $pp) {
            $docLines[] = $pp[1];
        }
        if ($retDoc !== null) {
            $docLines[] = "     * @return $retDoc";
        }
        $docLines[] = '     *';
        $docLines[] = '     * @throws \\Payrudder\\Exception\\PayrudderException';

        $sig = implode(', ', array_map(static fn($p) => $p[0], $params));
        $m = "    /**\n" . implode("\n", $docLines) . "\n     */\n";
        $m .= '    public function ' . $op['id'] . "($sig): $retDecl\n    {\n";
        $query = $queryMap === [] ? '[]' : '[' . implode(', ', $queryMap) . ']';
        $call = "\$this->$callName('" . $op['method'] . "', $pathExpr, $query, $bodyArg, \$options)";
        if ($ret['kind'] === 'void') {
            $m .= "        $call;\n    }\n";
        } elseif ($ret['kind'] === 'raw') {
            $m .= "        return $call;\n    }\n";
        } elseif ($ret['type']['kind'] === 'model') {
            $m .= '        return \\Payrudder\\Model\\' . $ret['type']['class'] . "::fromArray($call);\n    }\n";
        } else {
            $m .= "        return $call;\n    }\n";
        }

        // pagination helpers
        $pagination = $this->pagination($op);
        if ($pagination !== null) {
            [$itemsProp, $itemType] = $pagination;
            $respClass = '\\Payrudder\\Model\\' . $op['returns']['type']['class'];
            $sigP = $sig;
            $fwd = implode(', ', $callArgsForward);
            $docParams = implode("\n", array_map(static fn($p) => $p[1], $params));
            $m .= "\n    /**\n     * Walks every page of {$op['id']}, yielding one response per page. A page token passed in starts the walk there.\n     *\n$docParams\n     * @return \\Generator<int, $respClass>\n     */\n";
            $m .= '    public function ' . $op['id'] . "Pages($sigP): \\Generator\n    {\n";
            $m .= "        do {\n            \$page = \$this->{$op['id']}($fwd);\n            yield \$page;\n            \$pageToken = \$page->nextPageToken;\n        } while (\$pageToken !== null && \$pageToken !== '');\n    }\n";
            $m .= "\n    /**\n     * Walks every page of {$op['id']} and yields the items one by one.\n     *\n$docParams\n     * @return \\Generator<int, $itemType>\n     */\n";
            $m .= '    public function ' . $op['id'] . "Iterator($sigP): \\Generator\n    {\n";
            $m .= "        foreach (\$this->{$op['id']}Pages($fwd) as \$page) {\n            foreach (\$page->$itemsProp ?? [] as \$item) {\n                yield \$item;\n            }\n        }\n    }\n";
        }

        return $m;
    }

    /** @return array{0:string,1:string}|null */
    private function pagination(array $op): ?array
    {
        $hasToken = false;
        foreach ($op['params'] as $p) {
            if ($p['in'] === 'query' && $p['name'] === 'page_token') {
                $hasToken = true;
            }
        }
        if (!$hasToken || ($op['returns']['kind'] ?? '') !== 'json' || $op['returns']['type']['kind'] !== 'model') {
            return null;
        }
        $model = $this->models[$op['returns']['type']['class']];
        if (!isset($model['props']['next_page_token'])) {
            $this->warnings[$op['id']] = 'has page_token but the response has no next_page_token';

            return null;
        }
        foreach ($model['props'] as $n => $t) {
            if ($t['kind'] === 'list') {
                $item = $t['item'];
                $doc = $item['kind'] === 'model' ? '\\Payrudder\\Model\\' . $item['class'] : $this->doc($item);

                return [$this->camel($n), $doc];
            }
        }
        $this->warnings[$op['id']] = 'paginated response without an array property';

        return null;
    }

    // ---------------------------------------------------------------- files

    private function cleanDir(string $dir): void
    {
        if (!is_dir($dir)) {
            mkdir($dir, 0o777, true);

            return;
        }
        foreach (glob($dir . '/*.php') ?: [] as $f) {
            unlink($f);
        }
    }

    private function write(string $rel, string $content): void
    {
        $path = $this->root . '/' . $rel;
        if (!is_dir(dirname($path))) {
            mkdir(dirname($path), 0o777, true);
        }
        file_put_contents($path, $content);
    }
}

(new Gen(dirname(__DIR__)))->run();
