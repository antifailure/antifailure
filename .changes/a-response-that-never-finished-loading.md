# fixed

A successful HTTP response with a missing tRPC result left console cards
loading forever. Queries and mutations now reject malformed or incomplete
responses with a human error and the existing retry action. Valid empty and
nullable results are preserved, as are the server's permission refusals.
