# added

Continuous deployment can reach production. `cd.yml` updates the container app,
starts the bootstrap job and shifts ingress traffic, and every one of those is a
write the deploying identity held no permission for, so the production job would
have failed at its first call. The grant is an `azurerm_role_assignment` in the
stack rather than a command somebody ran once: Contributor on the production
resource group and nothing else, reviewable in the same place as everything else
about the environment, and present in state so a rebuild does not silently leave
it behind.
