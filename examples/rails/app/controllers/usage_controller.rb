class UsageController < ApplicationController
  # POST /usage {"customer": "cus_42", "meter": "api_calls", "quantity": "1"} counts one event for that customer.
  def create
    customer, meter = params[:customer], params[:meter]
    return render json: { error: "customer and meter are required" }, status: :bad_request unless customer.is_a?(String) && meter.is_a?(String)

    # A key per request makes a retry of this call count once. A real app derives it from what happened (an order id), not from the clock.
    key = request.headers["Idempotency-Key"].presence || SecureRandom.uuid
    result = client.ingest(customer_external_id: customer, meter_code: meter, quantity: (params[:quantity].presence || "1").to_s, event_time: Time.now.utc, idempotency_key: key)
    render json: result
  rescue Payrudder::Client::Error => e
    render json: { error: e.key || e.message }, status: :bad_gateway
  end

  private

  def client
    c = Rails.configuration.x.payrudder
    Payrudder::Client.new(api_key: c.api_key, base_url: c.base_url)
  end
end
