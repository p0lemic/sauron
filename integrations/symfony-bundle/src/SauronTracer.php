<?php

declare(strict_types=1);

namespace Sauron\Bundle;

/**
 * Developer-facing API for manual span instrumentation inside controllers and services.
 *
 * Usage:
 *
 *   class ProductController {
 *       public function __construct(private SauronTracer $tracer) {}
 *
 *       public function index(): JsonResponse {
 *           $products = $this->tracer->trace('fetch-products', fn() => $this->repo->findAll());
 *           $dto      = $this->tracer->trace('serialize', fn() => $this->serializer->normalize($products));
 *           return $this->json($dto);
 *       }
 *   }
 *
 * Spans are recorded as children of the current controller span and appear in the
 * Sauron waterfall between the DB/VIEW spans.
 */
final class SauronTracer
{
    public function __construct(private readonly SauronClient $client) {}

    /**
     * Measure a callable and emit a span with the given name.
     *
     * @template T
     * @param  callable(): T          $fn
     * @param  array<string, string>  $attributes
     * @return T
     */
    public function trace(
        string   $name,
        callable $fn,
        string   $kind       = 'custom',
        array    $attributes = [],
    ): mixed {
        if (!$this->client->hasActiveTrace()) {
            return $fn();
        }

        $start  = microtime(true);
        $status = 'ok';

        try {
            return $fn();
        } catch (\Throwable $e) {
            $status = 'error';
            $attributes['exception.message'] = $e->getMessage();
            $attributes['exception.class']   = $e::class;
            throw $e;
        } finally {
            $duration = (microtime(true) - $start) * 1000;
            $startDt  = \DateTimeImmutable::createFromFormat('U.u', number_format($start, 6, '.', ''));

            $this->client->recordSpan(
                name:       $name,
                kind:       $kind,
                startTime:  $startDt !== false ? $startDt : new \DateTimeImmutable(),
                durationMs: $duration,
                attributes: $attributes,
                status:     $status,
            );
        }
    }
}
