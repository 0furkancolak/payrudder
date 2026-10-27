<?php

declare(strict_types=1);

namespace Payrudder\Resource;

use InvalidArgumentException;
use Payrudder\Exception\PayrudderException;
use Payrudder\Model\UploadAssetsResponse;
use Payrudder\RequestOptions;

/**
 * The multipart upload of `$client->assets` (POST /v1/assets). Up to 20 files, 25 MiB each; use an
 * upload session for larger files.
 */
trait AssetsUploads
{
    /**
     * Uploads files in one multipart/form-data request.
     *
     * @param list<array{filename: string, content: string, content_type?: string}|string> $files a file is either a path on disk or an array with the bytes
     * @param array<string,mixed>|null $metadata e.g. ['files' => ['logo.png' => ['purpose' => 'invoice_logo']]]
     *
     * @throws InvalidArgumentException
     * @throws PayrudderException
     */
    public function uploadAssets(array $files, ?array $metadata = null, ?RequestOptions $options = null): UploadAssetsResponse
    {
        if ($files === []) {
            throw new InvalidArgumentException('at least one file is required');
        }
        $boundary = 'payrudder-' . bin2hex(random_bytes(12));
        $body = '';
        if ($metadata !== null) {
            $body .= "--$boundary\r\nContent-Disposition: form-data; name=\"metadata\"\r\nContent-Type: application/json\r\n\r\n"
                . json_encode($metadata, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE) . "\r\n";
        }
        foreach ($files as $file) {
            if (\is_string($file)) {
                $content = @file_get_contents($file);
                if ($content === false) {
                    throw new InvalidArgumentException("cannot read $file");
                }
                $file = ['filename' => basename($file), 'content' => $content];
            }
            $name = str_replace(['"', "\r", "\n"], ['%22', '', ''], $file['filename']);
            $type = $file['content_type'] ?? 'application/octet-stream';
            $body .= "--$boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"$name\"\r\nContent-Type: $type\r\n\r\n{$file['content']}\r\n";
        }
        $body .= "--$boundary--\r\n";

        $data = $this->http->jsonWithPayload('POST', '/v1/assets', $body, 'multipart/form-data; boundary=' . $boundary, [], $options);

        return UploadAssetsResponse::fromArray($data);
    }
}
