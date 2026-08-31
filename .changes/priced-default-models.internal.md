# added

The proxy and the runner pick a model from the same two variables in the same
order and fall back to the same two names, in two languages, and the control
plane refuses any model it has no price for. A test now calls the real selector,
reads the runner's defaults and `DEFAULT_PRICES`, and fails if the two sides
disagree or if either default is unpriced.
