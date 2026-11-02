require "json"
require "openssl"

module Payrudder
  # Verifies a delivery the way docs/16-webhooks.md describes: the header is `t=<unix>,v1=<hex>[,v1=<hex>]` and each v1 is
  # HMAC-SHA256(secret, "<t>.<raw body>"). There is no Ruby SDK, so this is the whole of it.
  module Webhook
    # The delivery cannot be trusted or read: answer 400.
    class Error < StandardError; end

    # Deliveries older (or newer) than this many seconds are refused, so a captured one cannot be replayed later.
    TOLERANCE = 300

    # `raw_body` must be the bytes as they arrived (request.raw_post), not a re-serialised params hash.
    def self.construct_event(raw_body, header, secret, now: Time.now.to_i, tolerance: TOLERANCE)
      # An empty secret would make every forged signature valid: that is a mistake in the app's settings, not a bad delivery.
      raise ArgumentError, "PAYRUDDER_WEBHOOK_SECRET is not set" if secret.to_s.empty?
      raise Error, "missing Payrudder-Signature header" if header.to_s.empty?

      parts = header.split(",").map { |p| p.strip.split("=", 2) }
      t = parts.find { |k, _| k == "t" }&.last.to_s
      raise Error, "malformed Payrudder-Signature header" unless t.match?(/\A\d+\z/)
      raise Error, "timestamp outside the tolerance" if (now - t.to_i).abs > tolerance

      signed = "#{t}.".b + raw_body.to_s.b
      want = OpenSSL::HMAC.hexdigest("SHA256", secret, signed)
      candidates = parts.select { |k, _| k == "v1" }.map { |_, v| v.to_s }
      raise Error, "no signature matches" unless candidates.any? { |v| ActiveSupport::SecurityUtils.secure_compare(v, want) }

      JSON.parse(raw_body)
    rescue JSON::ParserError
      raise Error, "the body is not JSON"
    end
  end
end
