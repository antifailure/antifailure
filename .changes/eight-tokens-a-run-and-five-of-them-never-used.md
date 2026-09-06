# fixed

Every `af` process on a CI runner minted an engine token when it started,
whether or not it ever sent anything to the control plane. A workflow run
minted eight and used three, and the token directory at `/cli` filled with
credentials issued for messages that did not exist, burying the one a person
had created under hundreds nobody had asked for.

The credential is now obtained on the first request rather than at startup,
through the same path that already renews an expired one. A process that
sends nothing mints nothing. A failed first attempt is reported the way it
always was, with the exchange's own reason, and is not repeated inside the
minute for a batch the sink retries.
