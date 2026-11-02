require "json"
require "net/http"

module Payrudder
  # The one call this app makes: POST /v1/events. There is no Ruby SDK, so this is plain Net::HTTP. It does not retry: the idempotency key
  # in the event is what makes a retry by the caller safe (the SDKs retry 429 and 5xx for you; here it is your job).
  class Client
    class Error < StandardError
      attr_reader :key, :status

      def initialize(message, key: nil, status: nil)
        super(message)
        @key = key
        @status = status
      end
    end

    def initialize(api_key:, base_url:)
      raise ArgumentError, "PAYRUDDER_API_KEY is not set" if api_key.to_s.empty?

      @api_key = api_key
      @base = base_url.to_s.chomp("/")
    end

    # Sends one usage event and returns its result ({"status" => "accepted", ...}, or "rejected" with an error_code: a bad event does not fail the call).
    def ingest(customer_external_id:, meter_code:, quantity:, event_time:, idempotency_key:)
      event = { customer_external_id:, meter_code:, quantity:, event_time: event_time.utc.iso8601(3), idempotency_key: }
      post("/v1/events", { events: [event] }).fetch("results").first
    end

    private

    def post(path, body)
      uri = URI("#{@base}#{path}")
      request = Net::HTTP::Post.new(uri, "Authorization" => "Bearer #{@api_key}", "Content-Type" => "application/json", "Accept" => "application/json", "User-Agent" => "payrudder-rails-example")
      request.body = JSON.generate(body)
      response = Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https", open_timeout: 5, read_timeout: 15) { |http| http.request(request) }
      parsed = begin
        JSON.parse(response.body.to_s)
      rescue JSON::ParserError
        nil
      end
      return parsed if response.is_a?(Net::HTTPSuccess) && parsed.is_a?(Hash)

      error = parsed.is_a?(Hash) ? parsed["error"] : nil
      raise Error.new(error.is_a?(Hash) ? error["message"].to_s : "the API answered #{response.code}", key: error.is_a?(Hash) ? error["key"] : nil, status: response.code.to_i)
    rescue Timeout::Error, SystemCallError, SocketError, OpenSSL::SSL::SSLError => e
      raise Error.new("could not reach the API: #{e.class}")
    end
  end
end
