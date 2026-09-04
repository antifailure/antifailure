# fixed

- Removing an environment or repository erased its consumption from operator
  analytics and could reset the rolling cost cap. Usage now retains each
  environment's recorded interval through cleanup, while organization deletion
  still erases its history. Scheduled maintenance saves daily UTC totals, and
  Analytics & Usage displays those measurements as a chart and accessible table.
  Already deleted history cannot be recovered.
