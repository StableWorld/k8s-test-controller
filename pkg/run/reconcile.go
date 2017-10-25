package run

import (
	"fmt"
	"log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"

	v1alpha1 "github.com/srossross/k8s-test-controller/pkg/apis/tester/v1alpha1"
	controller "github.com/srossross/k8s-test-controller/pkg/controller"
)

// UpdateTestRun will Reconcile a single test run
func (r *runner) UpdateTestRun(ctrl controller.Interface, testRun *v1alpha1.TestRun) error {

	if testRun.Status.Status == v1alpha1.TestRunComplete {
		log.Printf("  | '%v/%v' is already Complete - Skipping", testRun.Namespace, testRun.Name)
		return nil
	} else if testRun.Status.Status == "" {
		testRunCopy := testRun.DeepCopy()

		testRun.Status.Status = v1alpha1.TestRunRunning
		log.Printf("Running '%v/%v'", testRun.Namespace, testRun.Name)
		if _, err := ctrl.SrossrossV1alpha1().TestRuns(testRun.Namespace).Update(testRunCopy); err != nil {
			return err
		}
	}

	log.Printf("  | %v/%v", testRun.Namespace, testRun.Name)

	selector, err := metav1.LabelSelectorAsSelector(testRun.Spec.Selector)
	if selector.String() == "" {
		selector = labels.Everything()
	}

	if err != nil {
		return fmt.Errorf("error getting test selector: %s", err.Error())
	}

	tests, err := ctrl.TestTemplateLister().TestTemplates(testRun.Namespace).List(selector)

	if err != nil {
		return fmt.Errorf("error getting list of tests: %s", err.Error())
	}

	log.Printf("  | Test Count: %v", len(tests))

	pods, err := ctrl.ListPods(testRun.Namespace, labels.Everything())

	if err != nil {
		return fmt.Errorf("Error getting list of pods: %s", err.Error())
	}

	pods = controller.TestRunFilter(pods, testRun.Name)

	log.Printf("  | Total Pod Count: %v", len(pods))

	podMap := make(map[string]*corev1.Pod)
	for _, pod := range pods {
		// log.Printf("  |  Pod: %v", pod.Name)
		podMap[pod.Labels["test-name"]] = pod
	}

	// FIXME: should be a default in the schema ...
	var JobsSlots int
	if testRun.Spec.MaxJobs > 0 {
		JobsSlots = testRun.Spec.MaxJobs
	} else {
		JobsSlots = 1
	}

	completedCount := 0
	failCount := 0
	for _, test := range tests {
		if JobsSlots <= 0 {
			log.Printf("  | No more jobs allowed (maxjobs: %v). Will wait for next event", testRun.Spec.MaxJobs)
			return nil
		}

		log.Printf("  | Test: %v", test.Name)

		if pod, ok := podMap[test.Name]; ok {
			log.Printf("  |         - Pod '%v' exists - Status: %v", pod.Name, pod.Status.Phase)
			switch pod.Status.Phase {
			case "Succeeded":
				completedCount++
				continue
			case "Failed":
				completedCount++
				failCount++
				continue
			case "Unknown":
				completedCount++
				failCount++
				continue
			// These are running and taking up a job slot!
			case "Pending":
				JobsSlots--
				continue
			case "Running":
				JobsSlots--
				continue
			}
		} else {
			err = CreateTestPod(ctrl, testRun, test)

			if err != nil {
				return err
			}

			JobsSlots--
		}
	}

	if completedCount == len(tests) {

		Message := fmt.Sprintf("Ran %v tests, %v failures", completedCount, failCount)
		var Reason string
		testRun = testRun.DeepCopy()

		testRun.Status.Status = v1alpha1.TestRunComplete
		testRun.Status.Success = failCount == 0
		testRun.Status.Message = Message

		log.Printf("Saving '%v/%v'", testRun.Namespace, testRun.Name)
		if _, err := ctrl.SrossrossV1alpha1().TestRuns(testRun.Namespace).Update(testRun); err != nil {
			return err
		}
		log.Printf("We are done here %v tests completed", completedCount)

		switch failCount == 0 {
		case true:
			Reason = "TestRunSuccess"
		case false:
			Reason = "TestRunFail"
		}
		return CreateTestRunEvent(ctrl, testRun, "", Reason, Message)

	}
	log.Printf("Completed %v of %v tests", completedCount, len(tests))

	return nil
}
