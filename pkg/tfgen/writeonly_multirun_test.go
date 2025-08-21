package tfgen

import (
	"io"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	shim "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
	shimschema "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim/schema"
)

// TestWriteOnlyFieldsMultipleRuns tests the behavior of write-only fields across multiple operations:
// 1. Create operation (equivalent to first pulumi up)
// 2. Read/Refresh operation (equivalent to second pulumi up)
// 3. Ensure write-only fields behave correctly
func TestWriteOnlyFieldsMultipleRuns(t *testing.T) {
	t.Parallel()

	// Create a provider with a resource that has both required and optional write-only fields
	p := (&shimschema.Provider{
		ResourcesMap: shimschema.ResourceMap{
			"test_res_write_only": (&shimschema.Resource{
				Schema: shimschema.SchemaMap{
					"id": (&shimschema.Schema{
						Type:     shim.TypeString,
						Computed: true,
					}).Shim(),
					"name": (&shimschema.Schema{
						Type:     shim.TypeString,
						Required: true,
					}).Shim(),
					"required_password": (&shimschema.Schema{
						Type:      shim.TypeString,
						Required:  true,
						WriteOnly: true,
					}).Shim(),
					"optional_secret": (&shimschema.Schema{
						Type:      shim.TypeString,
						Optional:  true,
						WriteOnly: true,
					}).Shim(),
				},
			}).Shim(),
		},
	}).Shim()

	resInfo := &tfbridge.ResourceInfo{
		Tok: "test:index:WriteOnlyResource",
	}

	nilSink := diag.DefaultSink(io.Discard, io.Discard, diag.FormatOptions{
		Color: colors.Never,
	})

	// Test 1: Schema generation should work (required write-only fields included)
	schemaResult, err := GenerateSchemaWithOptions(GenerateSchemaOptions{
		DiagnosticsSink: nilSink,
		ProviderInfo: tfbridge.ProviderInfo{
			Name: "test",
			P:    p,
			Resources: map[string]*tfbridge.ResourceInfo{
				"test_res_write_only": resInfo,
			},
		},
	})
	require.NoError(t, err)

	// Verify schema generation results
	spec := schemaResult.PackageSpec
	require.Len(t, spec.Resources, 1)
	resourceSpec := spec.Resources["test:index:WriteOnlyResource"]
	require.NotNil(t, resourceSpec)

	// Required write-only field should be included, optional should be omitted
	require.Contains(t, resourceSpec.InputProperties, "requiredPassword", "Required write-only field should be in input properties")
	require.Contains(t, resourceSpec.InputProperties, "name", "Normal required field should be in input properties")
	require.NotContains(t, resourceSpec.InputProperties, "optionalSecret", "Optional write-only field should be omitted")

	// Debug: print actual property names in schema
	t.Logf("Schema input properties:")
	for k := range resourceSpec.InputProperties {
		t.Logf("  - '%s'", k)
	}

	// Test 2: Simulate ExtractInputsFromOutputs behavior (like during refresh)
	
	// Simulate the outputs that might come from Terraform state (write-only fields absent)
	terraformStateOutputs := resource.PropertyMap{
		"id":   resource.NewStringProperty("test-123"),
		"name": resource.NewStringProperty("test-resource"),
		// Note: write-only fields (required_password, optional_secret) are NOT in Terraform state
	}

	// Simulate old inputs from previous run (including write-only values)
	oldInputs := resource.PropertyMap{
		"name":             resource.NewStringProperty("test-resource"),
		"requiredPassword": resource.NewStringProperty("secret123"),
		// optional_secret was omitted from schema, so not in old inputs
	}
	
	// Debug: check what we actually put in oldInputs
	t.Logf("Created old inputs: %+v", oldInputs)
	t.Logf("Old input keys:")
	for k := range oldInputs {
		t.Logf("  - '%s'", k)
	}

	// Extract inputs for refresh operation - this is what happens on subsequent runs
	extractedInputs, err := tfbridge.ExtractInputsFromOutputs(
		oldInputs,
		terraformStateOutputs,
		p.ResourcesMap().Get("test_res_write_only").Schema(),
		resInfo.Fields,
		true, // isRefresh = true (subsequent run)
	)
	require.NoError(t, err)

	// Debug: print what we got back
	t.Logf("Old inputs: %+v", oldInputs)
	t.Logf("Terraform state outputs: %+v", terraformStateOutputs)
	t.Logf("Extracted inputs: %+v", extractedInputs)
	
	// Debug: print the actual keys
	t.Logf("Extracted input keys:")
	for k := range extractedInputs {
		t.Logf("  - '%s'", k)
	}

	// Test 3: Verify behavior on refresh
	
	// For refresh operations, ExtractInputsFromOutputs should preserve inputs that were
	// in oldInputs, even if they're not in the Terraform state (like write-only fields)
	_, hasName := extractedInputs["name"]
	require.True(t, hasName, "Regular field should be preserved")
	
	_, hasRequiredPassword := extractedInputs["requiredPassword"]
	require.True(t, hasRequiredPassword, "Required write-only field should be preserved from old inputs")
	
	_, hasOptionalSecret := extractedInputs["optionalSecret"]
	require.False(t, hasOptionalSecret, "Optional write-only field should remain absent")

	// The values should be preserved from old inputs
	require.Equal(t, "test-resource", extractedInputs["name"].StringValue())
	if hasRequiredPassword {
		require.Equal(t, "secret123", extractedInputs["requiredPassword"].StringValue())
	}

	t.Logf("✅ Write-only fields behave correctly across multiple operations:")
	t.Logf("   - Required write-only fields are included in schema")
	t.Logf("   - Required write-only fields are preserved from old inputs during refresh")
	t.Logf("   - Optional write-only fields are omitted entirely")
}