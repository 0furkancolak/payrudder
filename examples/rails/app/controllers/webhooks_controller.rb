class WebhooksController < ApplicationController
  def create
    # The signature covers the exact bytes Payrudder sent: raw_post is the body as it arrived, never params.to_json.
    event = Payrudder::Webhook.construct_event(request.raw_post, request.headers["Payrudder-Signature"], Rails.configuration.x.payrudder.webhook_secret)
    Rails.logger.info("webhook #{event["id"]} #{event["type"]}")
    # Do the work after answering (enqueue a job), or the delivery may time out: Payrudder retries one that is not answered with a 2xx.
    render json: { id: event["id"] }
  rescue Payrudder::Webhook::Error => e
    render json: { error: e.message }, status: :bad_request
  end
end
