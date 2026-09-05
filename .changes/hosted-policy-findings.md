# fixed

- The hosted pull-request check ignored policy findings whenever the browser
  workflows passed. Failing findings now fail the check, and an incomplete load
  experiment remains inconclusive instead of being reported as a pass.
