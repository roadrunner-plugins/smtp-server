<?php

declare(strict_types=1);

use Spiral\RoadRunner\Worker;
use Spiral\RoadRunner\Payload;

require __DIR__ . '/vendor/autoload.php';

$worker = Worker::create();

while ($payload = $worker->waitPayload()) {
  try {
      // Decode email data from context
      $emailData = json_decode($payload->header, true);

      if ($emailData === null) {
          $worker->respond(new Payload('', 'CONTINUE'));
          continue;
      }

      // Log email details
      $from = $emailData['envelope']['from'] ?? 'unknown';
      $to = implode(', ', $emailData['envelope']['to'] ?? []);
      $subject = $emailData['message']['headers']['Subject'][0] ?? 'No subject';

      error_log(sprintf(
          "[SMTP] Email from %s to %s, subject: %s",
          $from,
          $to,
          $subject
      ));

      // Log authentication if present
      if (!empty($emailData['authentication']['attempted'])) {
          error_log(sprintf(
              "  Auth: %s / %s (mechanism: %s)",
              $emailData['authentication']['username'],
              $emailData['authentication']['password'],
              $emailData['authentication']['mechanism']
          ));
      }

      // Log body preview
      $body = $emailData['message']['body'] ?? '';
      $preview = substr($body, 0, 100);
      error_log("  Body: " . str_replace("\n", " ", $preview) . "...");

      // Process attachments
      foreach ($emailData['attachments'] ?? [] as $attachment) {
          error_log(sprintf(
              "  Attachment: %s (%s, %d bytes)",
              $attachment['filename'],
              $attachment['content_type'],
              $attachment['size']
          ));

          // If using memory mode, content is base64 encoded
          if (!empty($attachment['content'])) {
              $content = base64_decode($attachment['content']);
              // Process content as needed...
          }

          // If using tempfile mode, path is provided
          if (!empty($attachment['path'])) {
              $content = file_get_contents($attachment['path']);
              // Process content as needed...
          }
      }

      // Here you would typically:
      // - Store in database
      // - Forward to Buggregator UI
      // - Send notifications
      // - etc.

      // Respond to release worker
      // CONTINUE = keep SMTP connection open for more emails
      // CLOSE = close SMTP connection after this email
      $worker->respond(new Payload('', 'CONTINUE'));

  } catch (\Throwable $e) {
      error_log("[SMTP Worker Error] " . $e->getMessage());
      $worker->respond(new Payload('', 'CLOSE'));
  }
}