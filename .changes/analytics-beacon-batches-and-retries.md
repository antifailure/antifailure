# fixed

The site beacon batches, retries and computes a session. It sent one request per
event into an endpoint built to take twenty, lost every event whose request
failed, and treated the browser tab as the session, so a tab left open over a
weekend was one visit. A session now ends after thirty minutes idle and after a
day, which makes the identifier shorter lived as well as the count truer.
