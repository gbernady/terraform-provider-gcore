package gcore

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	gcorecloud "github.com/G-Core/gcorelabscloud-go"
	"github.com/G-Core/gcorelabscloud-go/gcore/loadbalancer/v1/lbpools"
	"github.com/hashicorp/go-cty/cty"

	"github.com/G-Core/gcorelabscloud-go/gcore/task/v1/tasks"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const (
	minWeight                      = 0
	maxWeight                      = 256
	LBMemberResourceTimeoutMinutes = 30
)

func resourceLBMember() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceLBMemberCreate,
		ReadContext:   resourceLBMemberRead,
		UpdateContext: resourceLBMemberUpdate,
		DeleteContext: resourceLBMemberDelete,
		Description:   "Represent load balancer member",
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(LBMemberResourceTimeoutMinutes * time.Minute),
			Delete: schema.DefaultTimeout(LBMemberResourceTimeoutMinutes * time.Minute),
			Update: schema.DefaultTimeout(LBMemberResourceTimeoutMinutes * time.Minute),
		},
		Importer: &schema.ResourceImporter{
			StateContext: func(ctx context.Context, d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
				projectID, regionID, memberID, lbPoolID, err := ImportStringParserExtended(d.Id())

				if err != nil {
					return nil, err
				}
				d.Set("project_id", projectID)
				d.Set("region_id", regionID)
				d.Set("pool_id", lbPoolID)
				d.SetId(memberID)

				return []*schema.ResourceData{d}, nil
			},
		},

		Schema: map[string]*schema.Schema{
			"project_id": &schema.Schema{
				Type:        schema.TypeInt,
				Description: "ID of the desired project to create load balancer member in. Alternative for `project_name`. One of them should be specified.",
				Optional:    true,
				ForceNew:    true,
				ExactlyOneOf: []string{
					"project_id",
					"project_name",
				},
				DiffSuppressFunc: suppressDiffProjectID,
			},
			"region_id": &schema.Schema{
				Type:        schema.TypeInt,
				Description: "ID of the desired region to create load balancer member in. Alternative for `region_name`. One of them should be specified.",
				Optional:    true,
				ForceNew:    true,
				ExactlyOneOf: []string{
					"region_id",
					"region_name",
				},
				DiffSuppressFunc: suppressDiffRegionID,
			},
			"project_name": &schema.Schema{
				Type:        schema.TypeString,
				Description: "Name of the desired project to create load balancer member in. Alternative for `project_id`. One of them should be specified.",
				Optional:    true,
				ForceNew:    true,
				ExactlyOneOf: []string{
					"project_id",
					"project_name",
				},
			},
			"region_name": &schema.Schema{
				Type:        schema.TypeString,
				Description: "Name of the desired region to create load balancer member in. Alternative for `region_id`. One of them should be specified.",
				Optional:    true,
				ForceNew:    true,
				ExactlyOneOf: []string{
					"region_id",
					"region_name",
				},
			},
			"pool_id": &schema.Schema{
				Type:        schema.TypeString,
				Description: "ID of the target load balancer pool to attach newly created member.",
				Required:    true,
				ForceNew:    true,
			},
			"address": &schema.Schema{
				Type:        schema.TypeString,
				Description: "IP address to communicate with real server.",
				Required:    true,
				ValidateDiagFunc: func(val interface{}, key cty.Path) diag.Diagnostics {
					v := val.(string)
					ip := net.ParseIP(v)
					if ip != nil {
						return diag.Diagnostics{}
					}

					return diag.FromErr(fmt.Errorf("%q must be a valid ip, got: %s", key, v))
				},
			},
			"protocol_port": &schema.Schema{
				Type:        schema.TypeInt,
				Description: "Port to communicate with real server.",
				Required:    true,
			},
			"weight": &schema.Schema{
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "Value between 0 and 256, default 1.",
				Default:     1,
				ValidateDiagFunc: func(val interface{}, path cty.Path) diag.Diagnostics {
					v := val.(int)
					if v >= minWeight && v <= maxWeight {
						return nil
					}
					return diag.Errorf("Valid values: %d to %d got: %d", minWeight, maxWeight, v)
				},
			},
			"subnet_id": &schema.Schema{
				Type:        schema.TypeString,
				Description: "ID of the subnet in which real server placed.",
				Optional:    true,
				Computed:    true,
			},
			"instance_id": &schema.Schema{
				Type:        schema.TypeString,
				Description: "ID of the gcore_instance.",
				Optional:    true,
			},
			"operating_status": &schema.Schema{
				Type:        schema.TypeString,
				Description: "Operating status of this member.",
				Computed:    true,
			},
			"last_updated": &schema.Schema{
				Type:        schema.TypeString,
				Description: "Datetime when load balancer member was updated at the last time.",
				Computed:    true,
			},
		},
	}
}

func resourceLBMemberCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Println("[DEBUG] Start LBMember creating")
	var diags diag.Diagnostics
	config := m.(*Config)
	provider := config.Provider

	client, err := CreateClient(provider, d, LBPoolsPoint, versionPointV1)
	if err != nil {
		return diag.FromErr(err)
	}

	opts := lbpools.CreatePoolMemberOpts{
		Address:      net.ParseIP(d.Get("address").(string)),
		ProtocolPort: d.Get("protocol_port").(int),
		Weight:       d.Get("weight").(int),
		SubnetID:     d.Get("subnet_id").(string),
		InstanceID:   d.Get("instance_id").(string),
	}
	timeout := int(d.Timeout(schema.TimeoutCreate).Seconds())
	rc := GetConflictRetryConfig(timeout)
	results, err := lbpools.CreateMember(client, d.Get("pool_id").(string), opts, &gcorecloud.RequestOpts{
		ConflictRetryAmount:   rc.Amount,
		ConflictRetryInterval: rc.Interval,
	}).Extract()
	if err != nil {
		return diag.FromErr(err)
	}

	taskID := results.Tasks[0]
	pmID, err := tasks.WaitTaskAndReturnResult(client, taskID, true, timeout, func(task tasks.TaskID) (interface{}, error) {
		taskInfo, err := tasks.Get(client, string(task)).Extract()
		if err != nil {
			return nil, fmt.Errorf("cannot get task with ID: %s. Error: %w", task, err)
		}
		pmID, err := lbpools.ExtractPoolMemberIDFromTask(taskInfo)
		if err != nil {
			return nil, fmt.Errorf("cannot retrieve LBMember ID from task info: %w", err)
		}
		return pmID, nil
	})

	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId(pmID.(string))
	resourceLBMemberRead(ctx, d, m)

	log.Printf("[DEBUG] Finish LBMember creating (%s)", pmID)
	return diags
}

func resourceLBMemberRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Println("[DEBUG] Start LBMember reading")
	var diags diag.Diagnostics
	config := m.(*Config)
	provider := config.Provider

	client, err := CreateClient(provider, d, LBPoolsPoint, versionPointV1)
	if err != nil {
		return diag.FromErr(err)
	}

	pool, err := lbpools.Get(client, d.Get("pool_id").(string)).Extract()
	if err != nil {
		return diag.FromErr(err)
	}

	mid := d.Id()
	for _, pm := range pool.Members {
		if mid == pm.ID {
			d.Set("address", pm.Address.String())
			d.Set("protocol_port", pm.ProtocolPort)
			d.Set("weight", pm.Weight)
			d.Set("subnet_id", pm.SubnetID)
			d.Set("instance_id", pm.InstanceID)
			d.Set("operating_status", pm.OperatingStatus)
		}
	}

	fields := []string{"project_id", "region_id"}
	revertState(d, &fields)

	log.Println("[DEBUG] Finish LBMember reading)")
	return diags
}

func resourceLBMemberUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Println("[DEBUG] Start LBMember updating")
	config := m.(*Config)
	provider := config.Provider

	client, err := CreateClient(provider, d, LBPoolsPoint, versionPointV1)
	if err != nil {
		return diag.FromErr(err)
	}

	pool, err := lbpools.Get(client, d.Get("pool_id").(string)).Extract()
	if err != nil {
		return diag.FromErr(err)
	}

	members := make([]lbpools.CreatePoolMemberOpts, len(pool.Members))
	for i, pm := range pool.Members {
		if pm.ID != d.Id() {
			members[i] = lbpools.CreatePoolMemberOpts{
				Address:      *pm.Address,
				ProtocolPort: pm.ProtocolPort,
				Weight:       pm.Weight,
				SubnetID:     pm.SubnetID,
				InstanceID:   pm.InstanceID,
				ID:           pm.ID,
			}
			continue
		}

		members[i] = lbpools.CreatePoolMemberOpts{
			Address:      net.ParseIP(d.Get("address").(string)),
			ProtocolPort: d.Get("protocol_port").(int),
			Weight:       d.Get("weight").(int),
			SubnetID:     d.Get("subnet_id").(string),
			InstanceID:   d.Get("instance_id").(string),
			ID:           d.Id(),
		}
	}

	opts := lbpools.UpdateOpts{Name: pool.Name, Members: members}
	timeout := int(d.Timeout(schema.TimeoutUpdate).Seconds())
	rc := GetConflictRetryConfig(timeout)
	results, err := lbpools.Update(client, pool.ID, opts, &gcorecloud.RequestOpts{
		ConflictRetryAmount:   rc.Amount,
		ConflictRetryInterval: rc.Interval,
	}).Extract()
	if err != nil {
		return diag.FromErr(err)
	}

	taskID := results.Tasks[0]
	_, err = tasks.WaitTaskAndReturnResult(client, taskID, true, timeout, func(task tasks.TaskID) (interface{}, error) {
		taskInfo, err := tasks.Get(client, string(task)).Extract()
		if err != nil {
			return nil, fmt.Errorf("cannot get task with ID: %s. Error: %w", task, err)
		}
		lbPoolID, err := lbpools.ExtractPoolMemberIDFromTask(taskInfo)
		if err != nil {
			return nil, fmt.Errorf("cannot retrieve LBPool ID from task info: %w, %+v, %+v", err, taskInfo, task)
		}
		return lbPoolID, nil
	})
	if err != nil {
		return diag.FromErr(err)
	}

	d.Set("last_updated", time.Now().Format(time.RFC850))
	log.Println("[DEBUG] Finish LBMember updating")
	return resourceLBMemberRead(ctx, d, m)
}

func resourceLBMemberDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	log.Println("[DEBUG] Start LBMember deleting")
	var diags diag.Diagnostics
	config := m.(*Config)
	provider := config.Provider

	client, err := CreateClient(provider, d, LBPoolsPoint, versionPointV1)
	if err != nil {
		return diag.FromErr(err)
	}

	mid := d.Id()
	pid := d.Get("pool_id").(string)
	timeout := int(d.Timeout(schema.TimeoutDelete).Seconds())
	rc := GetConflictRetryConfig(timeout)
	results, err := lbpools.DeleteMember(client, pid, mid, &gcorecloud.RequestOpts{
		ConflictRetryAmount:   rc.Amount,
		ConflictRetryInterval: rc.Interval,
	}).Extract()
	if err != nil {
		switch err.(type) {
		case gcorecloud.ErrDefault404:
			d.SetId("")
			log.Printf("[DEBUG] Finish of LBMember deleting")
			return diags
		default:
			return diag.FromErr(err)
		}
	}

	taskID := results.Tasks[0]
	_, err = tasks.WaitTaskAndReturnResult(client, taskID, true, timeout, func(task tasks.TaskID) (interface{}, error) {
		pool, err := lbpools.Get(client, pid).Extract()
		if err != nil {
			return nil, err
		}

		for _, pm := range pool.Members {
			if pm.ID == mid {
				return nil, fmt.Errorf("pool member %s still exist", mid)
			}
		}

		return nil, nil
	})
	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId("")
	log.Printf("[DEBUG] Finish of LBMember deleting")
	return diags
}
