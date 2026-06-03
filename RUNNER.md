- To Create action runner controller if not already created.
```console
helm repo add actions-runner-controller https://actions-runner-controller.github.io/actions-runner-controller

helm repo update

helm upgrade --install --namespace actions-runner-system --create-namespace --wait actions-runner-controller actions-runner-controller/actions-runner-controller --set syncPeriod=1m

kubectl --namespace actions-runner-system get all
```
From nudgebee/deploy folder execute:
~~~
helm install self-host-runner --generate-name
~~~

### References
- https://github.com/actions/actions-runner-controller
- https://docs.github.com/en/actions/learn-github-actions/understanding-github-actions
- https://kubernetes.io/docs/tasks/run-application/run-single-instance-stateful-application/
- https://github.com/actions/actions-runner-controller/blob/master/docs/automatically-scaling-runners.md