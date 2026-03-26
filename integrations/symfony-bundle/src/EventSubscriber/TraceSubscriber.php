<?php

declare(strict_types=1);

namespace Sauron\Bundle\EventSubscriber;

use Sauron\Bundle\SauronClient;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Component\HttpKernel\Event\ControllerEvent;
use Symfony\Component\HttpKernel\Event\ExceptionEvent;
use Symfony\Component\HttpKernel\Event\RequestEvent;
use Symfony\Component\HttpKernel\Event\ResponseEvent;
use Symfony\Component\HttpKernel\Event\TerminateEvent;
use Symfony\Component\HttpKernel\Event\ViewEvent;
use Symfony\Component\HttpKernel\KernelEvents;

/**
 * Creates spans for the Symfony request lifecycle and wires up W3C trace context.
 *
 * Lifecycle (with optional routing + view spans enabled):
 *   kernel.request     → record routing span start time (before all other listeners)
 *   kernel.controller  → close routing span; extract traceparent; start controller span
 *   kernel.view        → close controller span early? No — record view span start
 *   kernel.response    → close controller span (+ view span if active)
 *   kernel.terminate   → flush all buffered spans to Sauron
 */
final class TraceSubscriber implements EventSubscriberInterface
{
    private const ATTR_CTRL_SPAN      = '_sauron_ctrl_span_id';
    private const ATTR_CTRL_START     = '_sauron_ctrl_start';
    private const ATTR_HAS_ERROR      = '_sauron_has_error';
    private const ATTR_ROUTING_START  = '_sauron_routing_start';
    private const ATTR_VIEW_START      = '_sauron_view_start';
    private const ATTR_VIEW_TEMPLATE  = '_sauron_view_template';
    private const ATTR_RESPONSE_READY = '_sauron_response_ready';

    public function __construct(
        private readonly SauronClient $client,
        private readonly bool $instrumentRouting = true,
        private readonly bool $instrumentView = true,
    ) {}

    public static function getSubscribedEvents(): array
    {
        return [
            KernelEvents::REQUEST    => ['onRequest', PHP_INT_MAX],
            KernelEvents::CONTROLLER => ['onController', 0],
            KernelEvents::VIEW       => ['onView', PHP_INT_MAX],
            KernelEvents::RESPONSE   => ['onResponse', 0],
            KernelEvents::EXCEPTION  => ['onException', 0],
            KernelEvents::TERMINATE  => ['onTerminate', -1024],
        ];
    }

    // ── Event handlers ────────────────────────────────────────────────────────

    /**
     * Fired before any other kernel.request listener — captures the routing start time.
     * The traceparent header is readable here already (set by the proxy upstream).
     */
    public function onRequest(RequestEvent $event): void
    {
        if (!$event->isMainRequest()) {
            return;
        }

        $traceparent = $event->getRequest()->headers->get('traceparent', '');
        [$traceId, $proxySpanId] = $this->parseTraceparent($traceparent);
        if ($traceId === '') {
            return;
        }

        // Boot span: covers PHP SAPI init + Composer autoload + Symfony DI boot,
        // from REQUEST_TIME_FLOAT (set by PHP-FPM on accept) to now (kernel.request fired).
        $phpStart = (float) ($_SERVER['REQUEST_TIME_FLOAT'] ?? microtime(true));
        $now      = microtime(true);

        $this->client->addSpan(
            traceId:      $traceId,
            spanId:       SauronClient::newSpanId(),
            parentSpanId: $proxySpanId,
            name:         'boot',
            kind:         'boot',
            startTime:    $this->floatToDateTime($phpStart),
            durationMs:   ($now - $phpStart) * 1000,
            attributes:   [
                'php.version'      => \PHP_VERSION,
                'php.sapi'         => \PHP_SAPI,
                'php.memory_limit' => \ini_get('memory_limit'),
                'php.peak_memory'  => $this->formatBytes(\memory_get_peak_usage(true)),
                'opcache.enabled'  => \function_exists('opcache_get_status') ? 'true' : 'false',
            ],
            status:       'ok',
        );

        if ($this->instrumentRouting) {
            // Routing starts exactly where boot ends — no gap, no overlap.
            $event->getRequest()->attributes->set(self::ATTR_ROUTING_START, $now);
        }
    }

    public function onController(ControllerEvent $event): void
    {
        if (!$event->isMainRequest()) {
            return;
        }

        $request     = $event->getRequest();
        $traceparent = $request->headers->get('traceparent', '');
        [$traceId, $proxySpanId] = $this->parseTraceparent($traceparent);

        if ($traceId === '') {
            return;
        }

        // Close the routing span (kernel.request → kernel.controller).
        if ($this->instrumentRouting) {
            $routingStart = $request->attributes->get(self::ATTR_ROUTING_START);
            if ($routingStart !== null) {
                $routingDuration = (microtime(true) - $routingStart) * 1000;
                $this->client->addSpan(
                    traceId:      $traceId,
                    spanId:       SauronClient::newSpanId(),
                    parentSpanId: $proxySpanId,
                    name:         'routing',
                    kind:         'routing',
                    startTime:    $this->floatToDateTime($routingStart),
                    durationMs:   $routingDuration,
                    attributes:   [
                        'http.method' => $request->getMethod(),
                        'http.path'   => $request->getPathInfo(),
                    ],
                    status: 'ok',
                );
            }
        }

        $ctrlSpanId = SauronClient::newSpanId();

        // Set active context on the client so Doctrine middleware can read it.
        $this->client->setActiveContext($traceId, $proxySpanId);
        $this->client->setActiveParentSpan($ctrlSpanId);

        $request->attributes->set(self::ATTR_CTRL_SPAN,  $ctrlSpanId);
        $request->attributes->set(self::ATTR_CTRL_START, microtime(true));
        $request->attributes->set(self::ATTR_HAS_ERROR,  false);
    }

    /**
     * kernel.view fires when the controller returned a non-Response value
     * (e.g. a ViewModel or array that Twig will render).
     */
    public function onView(ViewEvent $event): void
    {
        if (!$event->isMainRequest() || !$this->instrumentView) {
            return;
        }
        if (!$this->client->hasActiveTrace()) {
            return;
        }

        $result   = $event->getControllerResult();
        $template = \is_array($result) && isset($result['_template']) ? (string) $result['_template'] : '';

        $event->getRequest()->attributes->set(self::ATTR_VIEW_START,    microtime(true));
        $event->getRequest()->attributes->set(self::ATTR_VIEW_TEMPLATE, $template);
    }

    public function onResponse(ResponseEvent $event): void
    {
        if (!$event->isMainRequest()) {
            return;
        }

        $request   = $event->getRequest();
        $spanId    = $request->attributes->get(self::ATTR_CTRL_SPAN);
        $startedAt = $request->attributes->get(self::ATTR_CTRL_START);

        if ($spanId === null || $startedAt === null || !$this->client->hasActiveTrace()) {
            return;
        }

        $now        = microtime(true);
        $statusCode = $event->getResponse()->getStatusCode();
        $hasError   = $request->attributes->get(self::ATTR_HAS_ERROR, false);
        $status     = ($statusCode >= 400 || $hasError) ? 'error' : 'ok';
        $traceId    = $this->client->getActiveTraceId();
        $proxySpan  = $this->getProxySpanId($request);

        // If a view span was started, record it first (covers template rendering).
        if ($this->instrumentView) {
            $viewStart = $request->attributes->get(self::ATTR_VIEW_START);
            if ($viewStart !== null) {
                $this->client->addSpan(
                    traceId:      $traceId,
                    spanId:       SauronClient::newSpanId(),
                    parentSpanId: $spanId,  // child of controller span
                    name:         'view',
                    kind:         'view',
                    startTime:    $this->floatToDateTime($viewStart),
                    durationMs:   ($now - $viewStart) * 1000,
                    attributes:   array_filter([
                        'twig.template' => $request->attributes->get(self::ATTR_VIEW_TEMPLATE, ''),
                    ]),
                    status:       $status,
                );
            }
        }

        $durationMs = ($now - $startedAt) * 1000;
        $startTime  = $this->floatToDateTime($startedAt);

        $this->client->addSpan(
            traceId:      $traceId,
            spanId:       $spanId,
            parentSpanId: $proxySpan,
            name:         $this->resolveControllerName($request),
            kind:         'controller',
            startTime:    $startTime,
            durationMs:   $durationMs,
            attributes:   [
                'http.method'      => $request->getMethod(),
                'http.status_code' => (string) $statusCode,
                'http.route'       => (string) $request->attributes->get('_route', ''),
            ],
            status: $status,
        );

        // Save timestamp here — AFTER all spans are buffered — to mark when PHP finished.
        // onTerminate will use this to create the "send" span covering FastCGI transmission.
        $request->attributes->set(self::ATTR_RESPONSE_READY, microtime(true));
    }

    public function onException(ExceptionEvent $event): void
    {
        if (!$event->isMainRequest()) {
            return;
        }
        $event->getRequest()->attributes->set(self::ATTR_HAS_ERROR, true);
    }

    public function onTerminate(TerminateEvent $event): void
    {
        $request       = $event->getRequest();
        $responseReady = $request->attributes->get(self::ATTR_RESPONSE_READY);

        // "send" span: covers FastCGI serialisation + nginx forwarding to proxy.
        // kernel.terminate fires AFTER the FastCGI connection closes, so $now ≈ proxy HTTP span end.
        if ($responseReady !== null && $this->client->hasActiveTrace()) {
            $now = microtime(true);
            $this->client->addSpan(
                traceId:      $this->client->getActiveTraceId(),
                spanId:       SauronClient::newSpanId(),
                parentSpanId: $this->getProxySpanId($request),
                name:         'send',
                kind:         'send',
                startTime:    $this->floatToDateTime($responseReady),
                durationMs:   ($now - $responseReady) * 1000,
                attributes:   [],
                status:       'ok',
            );
        }

        $this->client->flush();
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    /**
     * Parse a W3C traceparent header: 00-{traceId(32)}-{spanId(16)}-{flags(2)}
     *
     * @return array{string, string}
     */
    private function parseTraceparent(string $header): array
    {
        if ($header === '') {
            return ['', ''];
        }
        $parts = explode('-', $header);
        if (\count($parts) !== 4) {
            return ['', ''];
        }
        [, $traceId, $parentId] = $parts;
        if (\strlen($traceId) !== 32 || \strlen($parentId) !== 16) {
            return ['', ''];
        }
        // Reject all-zero IDs (invalid per W3C spec).
        if (ltrim($traceId, '0') === '' || ltrim($parentId, '0') === '') {
            return ['', ''];
        }
        return [$traceId, $parentId];
    }

    private function getProxySpanId(\Symfony\Component\HttpFoundation\Request $request): string
    {
        $traceparent = $request->headers->get('traceparent', '');
        [, $proxySpanId] = $this->parseTraceparent($traceparent);
        return $proxySpanId;
    }

    private function resolveControllerName(\Symfony\Component\HttpFoundation\Request $request): string
    {
        $controller = $request->attributes->get('_controller', '');
        if (\is_string($controller) && $controller !== '') {
            return $controller;
        }
        if (\is_array($controller) && isset($controller[0], $controller[1])) {
            $class = \is_object($controller[0]) ? $controller[0]::class : (string) $controller[0];
            return $class . '::' . $controller[1];
        }
        return 'controller';
    }

    private function floatToDateTime(float $microtime): \DateTimeImmutable
    {
        $dt = \DateTimeImmutable::createFromFormat('U.u', number_format($microtime, 6, '.', ''));
        return $dt !== false ? $dt : new \DateTimeImmutable();
    }

    private function formatBytes(int $bytes): string
    {
        if ($bytes >= 1_048_576) {
            return round($bytes / 1_048_576, 1) . ' MB';
        }
        if ($bytes >= 1_024) {
            return round($bytes / 1_024, 1) . ' KB';
        }
        return $bytes . ' B';
    }
}
