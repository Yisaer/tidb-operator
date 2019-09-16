helm init --service-account tiller --upgrade -i registry.cn-hangzhou.aliyuncs.com/google_containers/tiller:v2.14.1 --skip-refresh

## init Password

kubectl create secret generic tidb-secret --from-literal=root=testphase --namespace=tidb