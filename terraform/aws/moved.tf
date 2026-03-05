moved {
  from = module.cross_account_iam
  to   = module.cross_account_iam[0]
}

moved {
  from = module.vpc
  to   = module.vpc[0]
}
