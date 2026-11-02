class ApplicationController < ActionController::API
  # The JSON body is not copied under a second key in the params (and so in the log).
  wrap_parameters false
end
