require_relative "boot"
require "rails"
require "action_controller/railtie"

Bundler.require(*Rails.groups)

module PayrudderRails
  class Application < Rails::Application
    config.load_defaults 8.1
    # An API: no views, sessions, cookies or CSRF token in front of a webhook (a delivery has none and would be refused).
    config.api_only = true
    config.eager_load = true
    config.enable_reloading = false
    config.logger = ActiveSupport::Logger.new($stdout)
    config.log_level = :info
    # Nothing here uses the secret (no cookies, no signed ids); Rails wants one to start in production.
    config.secret_key_base = ENV["SECRET_KEY_BASE"] || SecureRandom.hex(64)
    config.hosts.clear
    # A delivery's `data` holds customers' details; it stays out of the request log.
    config.filter_parameters += [:data]

    # The three Payrudder settings are read here, once, and handed to the client: the app does not reach into ENV from a controller.
    config.x.payrudder.api_key = ENV.fetch("PAYRUDDER_API_KEY", "")
    config.x.payrudder.base_url = ENV.fetch("PAYRUDDER_BASE_URL", "http://localhost:42800")
    config.x.payrudder.webhook_secret = ENV.fetch("PAYRUDDER_WEBHOOK_SECRET", "")
  end
end
