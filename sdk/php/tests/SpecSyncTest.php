<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Client;
use Payrudder\Internal\AbstractResource;
use Payrudder\Tools\SpecLoader;
use PHPUnit\Framework\TestCase;
use ReflectionMethod;
use ReflectionObject;
use ReflectionProperty;

/**
 * Fails when api/openapi/v1.yaml and the client disagree, so the SDK cannot drift silently.
 */
final class SpecSyncTest extends TestCase
{
    /** Methods that are helpers, not operations. */
    private const HELPERS = ['ingest'];

    /**
     * @return array<string,string> operationId => resource property name
     */
    private static function specOperations(): array
    {
        $spec = SpecLoader::load();
        $ops = [];
        foreach ($spec['paths'] as $path => $item) {
            if (!str_starts_with($path, '/v1/')) {
                continue;
            }
            foreach (['get', 'post', 'put', 'patch', 'delete'] as $method) {
                if (isset($item[$method])) {
                    $id = $item[$method]['operationId'] ?? null;
                    self::assertIsString($id, "$method $path has no operationId");
                    self::assertArrayNotHasKey($id, $ops, "duplicate operationId $id");
                    $segment = explode('/', trim($path, '/'))[1];
                    $ops[$id] = lcfirst(str_replace(' ', '', ucwords(str_replace('-', ' ', $segment))));
                }
            }
        }

        return $ops;
    }

    /**
     * @return array<string,string> method name => resource property name
     */
    private static function clientMethods(): array
    {
        $out = [];
        $client = new Client(apiKey: 'qk_test_x', baseUrl: 'http://localhost');
        foreach ((new ReflectionObject($client))->getProperties(ReflectionProperty::IS_PUBLIC) as $prop) {
            $resource = $prop->getValue($client);
            self::assertIsObject($resource);
            foreach ((new ReflectionObject($resource))->getMethods(ReflectionMethod::IS_PUBLIC) as $m) {
                if ($m->isConstructor() || $m->getDeclaringClass()->getName() === AbstractResource::class) {
                    continue;
                }
                $name = $m->getName();
                if (str_ends_with($name, 'Pages') || str_ends_with($name, 'Iterator') || \in_array($name, self::HELPERS, true)) {
                    continue;
                }
                self::assertArrayNotHasKey($name, $out, "method $name exists in two resources");
                $out[$name] = $prop->getName();
            }
        }

        return $out;
    }

    public function testEveryOperationHasAMethodInItsResource(): void
    {
        $methods = self::clientMethods();
        foreach (self::specOperations() as $id => $resource) {
            self::assertArrayHasKey($id, $methods, "operation $id has no client method");
            self::assertSame($resource, $methods[$id], "operation $id lives in the wrong resource");
        }
    }

    public function testEveryMethodIsAnOperation(): void
    {
        $ops = self::specOperations();
        foreach (array_keys(self::clientMethods()) as $name) {
            self::assertArrayHasKey($name, $ops, "client method $name is not an operation of api/openapi/v1.yaml");
        }
    }

    public function testTheSpecStillHasTheOperationsWeKnow(): void
    {
        // a guard against the loader silently returning an empty spec
        self::assertGreaterThan(100, \count(self::specOperations()));
    }
}
