Rails.application.routes.draw do
  post "/usage", to: "usage#create"
  post "/webhooks/payrudder", to: "webhooks#create"
  get "/healthz", to: "health#show"
end
