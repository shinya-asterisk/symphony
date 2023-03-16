/*
   MIT License

   Copyright (c) Microsoft Corporation.

   Permission is hereby granted, free of charge, to any person obtaining a copy
   of this software and associated documentation files (the "Software"), to deal
   in the Software without restriction, including without limitation the rights
   to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
   copies of the Software, and to permit persons to whom the Software is
   furnished to do so, subject to the following conditions:

   The above copyright notice and this permission notice shall be included in all
   copies or substantial portions of the Software.

   THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
   IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
   FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
   AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
   LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
   OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
   SOFTWARE

*/

package fabric

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/azure/symphony/api/pkg/apis/v1alpha1/model"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"sigs.k8s.io/controller-runtime/pkg/log"

	fabricv1 "gopls-workspace/apis/fabric/v1"
	utils "gopls-workspace/utils"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// TargetReconciler reconciles a Target object
type TargetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=fabric.symphony,resources=targets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fabric.symphony,resources=targets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fabric.symphony,resources=targets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Target object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *TargetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	myFinalizerName := "target.fabric.symphony/finalizer"

	log := ctrllog.FromContext(ctx)
	log.Info("Reconcile Target")

	// Get target
	target := &fabricv1.Target{}
	if err := r.Get(ctx, req.NamespacedName, target); err != nil {
		log.Error(err, "unable to fetch Target object")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	deployment, err := utils.CreateSymphonyDeploymentFromTarget(*target)
	if err != nil {
		log.Error(err, "failed to generate Symphony deployment")
		return ctrl.Result{}, err
	}

	if target.ObjectMeta.DeletionTimestamp.IsZero() { // update
		if !controllerutil.ContainsFinalizer(target, myFinalizerName) {
			controllerutil.AddFinalizer(target, myFinalizerName)
			if err := r.Update(ctx, target); err != nil {
				return ctrl.Result{}, err
			}
		}

		if len(deployment.Stages[0].Assignments) != 0 {
			summary, err := utils.Deploy(deployment)
			if err != nil {
				log.Error(err, "failed to deploy to Symphony")
				return ctrl.Result{}, r.updateTargetStatus(target, "Failed", summary)
			}

			if err := r.Update(ctx, target); err != nil {
				return ctrl.Result{}, r.updateTargetStatus(target, "State Failed", summary)
			} else {
				err = r.updateTargetStatus(target, "OK", summary)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}
		return ctrl.Result{RequeueAfter: 180 * time.Second}, nil

	} else { // remove
		if controllerutil.ContainsFinalizer(target, myFinalizerName) {
			summary, err := utils.Remove(deployment)
			if err != nil { // TODO: this could stop the CRD being removed if the underlying component is permanantly destroyed
				log.Error(err, "failed to delete components")
				return ctrl.Result{}, r.updateTargetStatus(target, "Remove Failed", summary)
			}

			controllerutil.RemoveFinalizer(target, myFinalizerName)
			if err := r.Update(ctx, target); err != nil {
				return ctrl.Result{}, r.updateTargetStatus(target, "State Failed", summary)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *TargetReconciler) updateTargetStatus(target *fabricv1.Target, status string, summary model.SummarySpec) error {
	if target.Status.Properties == nil {
		target.Status.Properties = make(map[string]string)
	}
	target.Status.Properties["status"] = status
	target.Status.Properties["targets"] = strconv.Itoa(summary.TargetCount)
	target.Status.Properties["deployed"] = strconv.Itoa(summary.SuccessCount)
	for k, v := range summary.TargetResults {
		target.Status.Properties["targets."+k] = fmt.Sprintf("%s - %s", v.Status, v.Message)
	}
	return r.Status().Update(context.Background(), target)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TargetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fabricv1.Target{}).
		Complete(r)
}
