package phases

import (
	"context"
	"fmt"

	"github.com/databricks/cli/bundle"
	"github.com/databricks/cli/bundle/artifacts"
	"github.com/databricks/cli/bundle/config"
	"github.com/databricks/cli/bundle/config/mutator"
	"github.com/databricks/cli/bundle/deploy"
	"github.com/databricks/cli/bundle/deploy/files"
	"github.com/databricks/cli/bundle/deploy/lock"
	"github.com/databricks/cli/bundle/deploy/metadata"
	"github.com/databricks/cli/bundle/deploy/terraform"
	"github.com/databricks/cli/bundle/libraries"
	"github.com/databricks/cli/bundle/permissions"
	"github.com/databricks/cli/bundle/python"
	"github.com/databricks/cli/bundle/scripts"
	"github.com/databricks/cli/libs/cmdio"
	terraformlib "github.com/databricks/cli/libs/terraform"
)

func approvalForDeploy(ctx context.Context, b *bundle.Bundle) (bool, error) {
	if b.AutoApprove {
		return true, nil
	}

	tf := b.Terraform
	if tf == nil {
		return false, fmt.Errorf("terraform not initialized")
	}

	// read plan file
	plan, err := tf.ShowPlanFile(ctx, b.Plan.Path)
	if err != nil {
		return false, err
	}

	// TODO: Is schema recreation possible? Would that be destructive to the data?
	deleteOrRecreateSchema := make([]terraformlib.Action, 0)
	for _, rc := range plan.ResourceChanges {
		if rc.Change.Actions.Delete() && rc.Type == "databricks_schema" {
			deleteOrRecreateSchema = append(deleteOrRecreateSchema, terraformlib.Action{
				Action:       "delete",
				ResourceType: rc.Type,
				ResourceName: rc.Name,
			})
		}

		if rc.Change.Actions.Replace() && rc.Type == "databricks_schema" {
			deleteOrRecreateSchema = append(deleteOrRecreateSchema, terraformlib.Action{
				Action:       "recreate",
				ResourceType: rc.Type,
				ResourceName: rc.Name,
			})
		}
	}

	// No need for approval if the plan does not include destroying or recreating
	// any schema resources.
	if len(deleteOrRecreateSchema) == 0 {
		return true, nil
	}

	// TODO: Return early with error here if the `--force-schema (or something)` delete
	// flag is not specified.
	for _, action := range deleteOrRecreateSchema {
		cmdio.LogString(ctx, "The following UC schemas will be deleted or recreated. Any underlying data will be lost:")

	if len(deleteActions) > 0 {
		cmdio.LogString(ctx, "")
		cmdio.LogString(ctx, "The following resources will be deleted:")
		for _, a := range deleteActions {
			cmdio.Log(ctx, a)
		}
	}

	if len(recreateActions) > 0 {
		cmdio.LogString(ctx, "")
		cmdio.LogString(ctx, "The following resources will be recreated. Note that recreation can be lossy and may lead to lost metadata or data:")
		for _, a := range recreateActions {
			cmdio.Log(ctx, a)
		}
		cmdio.LogString(ctx, "")
	}

	if !cmdio.IsPromptSupported(ctx) {
		return false, fmt.Errorf("the deployment requires destructive actions, but current console does not support prompting. Please specify --auto-approve if you would like to skip prompts and proceed")
	}

	cmdio.LogString(ctx, "")
	approved, err := cmdio.AskYesOrNo(ctx, "Would you like to proceed?")
	if err != nil {
		return false, err
	}

	return approved, nil
}

// The deploy phase deploys artifacts and resources.
func Deploy() bundle.Mutator {
	// Core mutators that CRUD resources and modify deployment state. These
	// mutators need informed consent if they are potentially destructive.
	deployCore := bundle.Defer(
		terraform.Apply(),
		bundle.Seq(
			terraform.StatePush(),
			terraform.Load(),
			metadata.Compute(),
			metadata.Upload(),
			bundle.LogString("Deployment complete!"),
		),
	)

	deployMutator := bundle.Seq(
		scripts.Execute(config.ScriptPreDeploy),
		lock.Acquire(),
		bundle.Defer(
			bundle.Seq(
				terraform.StatePull(),
				deploy.StatePull(),
				mutator.ValidateGitDetails(),
				libraries.ValidateLocalLibrariesExist(),
				artifacts.CleanUp(),
				artifacts.UploadAll(),
				python.TransformWheelTask(),
				files.Upload(),
				deploy.StateUpdate(),
				deploy.StatePush(),
				permissions.ApplyWorkspaceRootPermissions(),
				terraform.Interpolate(),
				terraform.Write(),
				terraform.CheckRunningResource(),
				terraform.Plan(terraform.PlanGoal("deploy")),
				terraform.Load(),
				bundle.If(
					approvalForDeploy,
					deployCore,
					bundle.LogString("Deployment cancelled!"),
				),
				lock.Release(lock.GoalDeploy),
			),
			scripts.Execute(config.ScriptPostDeploy),
		),
	)

	return newPhase(
		"deploy",
		[]bundle.Mutator{deployMutator},
	)
}
