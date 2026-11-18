package core

import (
	"path/filepath"
	"testing"
)

func TestOpsKind(t *testing.T) {
	for cmd, want := range map[string]string{
		// deploys
		"vercel":                                OpsDeploy,
		"vercel --prod":                         OpsDeploy,
		"vercel ./site":                         OpsDeploy,
		"vercel deploy --prebuilt":              OpsDeploy,
		"sls deploy --stage prod":               OpsDeploy,
		"serverless deploy function -f api":     OpsDeploy,
		"npx cdk deploy":                        OpsDeploy,
		"cdk deploy MyStack":                    OpsDeploy,
		"fly deploy":                            OpsDeploy,
		"firebase deploy --only hosting":        OpsDeploy,
		"kubectl -n prod apply -f k8s/api.yaml": OpsDeploy,
		"kubectl rollout restart deploy/api":    OpsDeploy,
		"helm upgrade api ./chart":              OpsDeploy,
		"terraform apply -auto-approve":         OpsDeploy,
		"terraform -chdir=infra apply":          OpsDeploy,
		"gcloud run deploy api --region us":     OpsDeploy,
		"aws lambda update-function-code --zip-file fileb://f.zip --function-name f": OpsDeploy,
		"aws --profile prod cloudformation deploy --template t.yml":                  OpsDeploy,
		"docker push registry.example.com/api:1":                                     OpsDeploy,
		"supabase db push":                                                           OpsDeploy,
		"supabase functions deploy hello":                                            OpsDeploy,
		"wrangler deploy":                                                            OpsDeploy,
		"npm run deploy":                                                             OpsDeploy,
		"yarn deploy:prod":                                                           OpsDeploy,
		"pnpm run deploy-staging":                                                    OpsDeploy,
		"npm publish":                                                                OpsDeploy,
		"make deploy ENV=prod":                                                       OpsDeploy,
		"./scripts/deploy.sh prod":                                                   OpsDeploy,
		"bash deploy.sh":                                                             OpsDeploy,
		"sh ./scripts/deploy-prod.sh":                                                OpsDeploy,
		"cargo publish":                                                              OpsDeploy,
		"npm run build":                                                              "",
		"npm run deployment-docs":                                                    "",
		"make test":                                                                  "",
		"yarn test":                                                                  "",
		"./scripts/redeploy-notes.sh":                                                "",
		// destroys
		"terraform destroy":         OpsDestroy,
		"kubectl delete pod api-1":  OpsDestroy,
		"kubectl delete pod status": OpsDestroy, // a name like a look word doesn't hide it
		"helm uninstall api":        OpsDestroy,
		"aws s3 rm s3://bucket/key": OpsDestroy,
		"aws --region us-east-1 ec2 terminate-instances --instance-ids i-1": OpsDestroy,
		"aws dynamodb delete-table --table-name t":                          OpsDestroy,
		"gh repo delete me/x":  OpsDestroy,
		"heroku apps:destroy":  OpsDestroy,
		"vercel remove my-app": OpsDestroy,
		// writes
		"aws s3 sync ./build s3://bucket":                                OpsWrite,
		"aws ssm put-parameter --name x":                                 OpsWrite,
		"heroku config:set A=1":                                          OpsWrite,
		"psql postgres://db.example.com/app -c \"update users set a=1\"": OpsWrite,
		"psql -h db.example.com app -c \"select 1; drop table users\"":   OpsWrite,
		"psql -h db.example.com -f fix.sql":                              OpsWrite,
		"psql -h db.example.com app":                                     OpsWrite, // interactive
		"psql -h db.example.com app < fix.sql":                           OpsWrite,
		"redis-cli -h cache.example.com FLUSHALL":                        OpsWrite,
		"mongosh mongodb://db.example.com":                               OpsWrite,
		"prisma migrate deploy":                                          OpsWrite,
		"curl -X POST https://api.example.com/x":                         OpsWrite,
		"gh api -X DELETE repos/me/x":                                    OpsWrite,
		"gh release create v1.0.0":                                       OpsWrite,
		"gh workflow run deploy.yml":                                     OpsWrite,
		"supabase db reset --linked":                                     OpsWrite,
		// remote
		"ssh prod-1 'sudo systemctl restart api'": OpsRemote,
		"scp build.tar prod-1:/srv":               OpsRemote,
		"rsync -a build/ prod-1:/srv/app":         OpsRemote,
		// reads and local work
		"aws s3 ls":                  "",
		"aws ec2 describe-instances": "",
		"aws --profile prod logs tail /aws/lambda/f": "",
		"kubectl get pods -n prod":                   "",
		"kubectl rollout status deploy/api":          "",
		"kubectl apply --dry-run=client -f k.yaml":   "",
		"helm list":                   "",
		"terraform plan":              "",
		"terraform state list":        "",
		"gh pr create --fill":         "",
		"gh release list":             "",
		"gh api repos/me/x":           "",
		"supabase secrets list":       "",
		"vercel env ls":               "",
		"vercel logs my-app":          "",
		"gcloud config set project x": "",
		"psql -h db.example.com app -c \"select * from users where note = 'update me'\"": "",
		"psql -h localhost app -c \"drop table x\"":                                      "",
		"redis-cli -h cache.example.com GET k":                                           "",
		"curl https://api.example.com/health":                                            "",
		"curl -X POST http://localhost:3000/x":                                           "",
		"docker build -t api .":                                                          "",
		"rsync -a build/ out/":                                                           "",
		"sls deploy --help":                                                              "",
		// found by the fresh-eyes review: runners, flag orderings, more tools
		"npx vercel --prod":                                           OpsDeploy,
		"yarn vercel --prod":                                          OpsDeploy,
		"pnpm exec vercel --prod":                                     OpsDeploy,
		"pnpm dlx vercel --prod":                                      OpsDeploy,
		"bunx vercel --prod":                                          OpsDeploy,
		"npm exec -- vercel --prod":                                   OpsDeploy,
		"npm --prefix infra run deploy":                               OpsDeploy,
		"npm run -w api deploy":                                       OpsDeploy,
		"yarn workspace api deploy":                                   OpsDeploy,
		"yarn --cwd infra deploy":                                     OpsDeploy,
		"pnpm --filter api run deploy":                                OpsDeploy,
		". ./scripts/deploy.sh":                                       OpsDeploy,
		"source scripts/deploy.sh":                                    OpsDeploy,
		"python scripts/deploy.py":                                    OpsDeploy,
		"node scripts/deploy.js":                                      OpsDeploy,
		"mvn deploy":                                                  OpsDeploy,
		"./gradlew publish":                                           OpsDeploy,
		"docker image push registry/x:1":                              OpsDeploy,
		"docker --debug push registry/x:1":                            OpsDeploy,
		"docker buildx build --push -t r/x .":                         OpsDeploy,
		"docker compose push":                                         OpsDeploy,
		"gcloud builds submit":                                        OpsDeploy,
		"kubectl apply -f k8s/ --dry-run=none":                        OpsDeploy,
		"terraform apply plan":                                        OpsDeploy,
		"eksctl create cluster -f c.yaml":                             OpsDeploy,
		"gcloud storage rm gs://x/y":                                  OpsDestroy,
		"gsutil rm -r gs://prod":                                      OpsDestroy,
		"aws kms schedule-key-deletion --key-id k":                    OpsDestroy,
		"aws ecr batch-delete-image --repository-name r":              OpsDestroy,
		"eksctl delete cluster prod":                                  OpsDestroy,
		"gh api repos/me/x/releases -f tag_name=v1":                   OpsWrite,
		"aws dynamodb batch-write-item --request-items file://x.json": OpsWrite,
		"aws batch submit-job --job-name j":                           OpsWrite,
		"gcloud projects remove-iam-policy-binding p":                 OpsWrite,
		"curl -XDELETE https://api.example.com/u/1":                   OpsWrite,
		"curl -sSd x https://api.example.com":                         OpsWrite,
		"curl --json '{}' https://api.example.com":                    OpsWrite,
		"http DELETE https://api.example.com/u/1":                     OpsWrite,
		"http POST api.example.com name=x":                            OpsWrite,
		"curl -X POST -H 'Origin: http://localhost:3000' https://api.prod.example.com": OpsWrite,
		`psql -h prod.example.com -c "delete from s where host='localhost'"`:           OpsWrite,
		`psql -h db.example.com -c "select pg_terminate_backend(1)"`:                   OpsWrite,
		"gcloud compute ssh vm-1":                              OpsRemote,
		"ansible-playbook -i prod site.yml":                    OpsRemote,
		"kubectl exec -it api-1 -- sh":                         OpsRemote,
		"npm publish --dry-run":                                "",
		"cargo publish --dry-run":                              "",
		"vercel build --prod":                                  "",
		"kubectl auth can-i create pods":                       "",
		"vercel --cwd apps/web env pull":                       "",
		"vercel --scope team ls":                               "",
		"kubectl config set current-context x":                 "",
		"pnpm deploy --filter api out":                         "",
		"make -C deploy lint":                                  "",
		"make -f deploy.mk test":                               "",
		"http GET https://api.example.com":                     "",
		"http https://api.example.com q==1":                    "",
		"curl -X GET https://api.example.com":                  "",
		"wget -T 5 https://example.com":                        "",
		"curl localhost:3000/x -d a=1":                         "",
		"http :3000/x name=y":                                  "",
		"yarn add deploy-tool":                                 "",
		"psql postgres://me@localhost/app -c \"drop table x\"": "",
		"gh api repos/me/x -X GET -f per_page=5":               "",
	} {
		segs := ParseShell(cmd, "/tmp")
		if len(segs) == 0 {
			t.Fatalf("%q: no segments", cmd)
		}
		if got := OpsKind(segs[0]); got != want {
			t.Errorf("OpsKind(%q) = %q, want %q", cmd, got, want)
		}
	}
}

const opsPlan = "# Plan\n## Files\n- src/a.go\n## Cases\n- [bug] A returns 2 -> test: test.sh\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- `aws s3 ls`\n- `sls deploy --stage prod`\n- `psql -h db.example.com app -c \"select count(*) from users\"`\n- `aws s3 rm s3://bucket/old.txt`\n"

func opsTask(t *testing.T, pipeline bool, parts map[string]bool) *harness {
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main", DeployPipeline: pipeline, Parts: parts}
	s.Commands.Test = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.docs(DocUnderstanding, "# U\nno existing code is involved\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, opsPlan)
	h.approve()
	return h
}

func TestRiskyApprovedCommandsAskEachTime(t *testing.T) {
	h := opsTask(t, false, nil)
	// Approved reads run as listed, except production database reads,
	// which are asked each time (a core rule).
	h.expect(h.bash("aws s3 ls"), "", "")
	h.expect(h.bash(`psql -h db.example.com app -c "select count(*) from users"`), "ask", "shared or production database")
	// Approved, but risky: confirmed every time.
	h.expect(h.bash("sls deploy --stage prod"), "ask", "it deploys")
	h.expect(h.bash("sls deploy --stage prod"), "ask", "confirmed each time")
	h.expect(h.bash("aws s3 rm s3://bucket/old.txt"), "ask", "deletes something")
	// Not in the plan: the usual outside-the-laptop prompt, also for
	// commands only phase 3 knows reach outside.
	h.expect(h.bash("aws s3 rm s3://bucket/other.txt"), "ask", "outside your laptop")
	h.expect(h.bash("npm run deploy"), "ask", "it deploys")
	h.expect(h.bash("rsync -a build/ prod-1:/srv/app"), "ask", "another machine")
	h.expect(h.bash("prisma migrate deploy"), "ask", "changes data")
	h.expect(h.bash("npm run build"), "", "")
	// A builder can't put a question to the user: it goes to the lead.
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	h.expect(h.hi(id, HelperBuilder, "Bash", map[string]any{"command": "cd " + wt + " && sls deploy --stage prod"}), "deny", "runs without the user")
}

func TestPipelineProjectsNeverDeployByHand(t *testing.T) {
	h := opsTask(t, true, nil)
	// Even when the plan lists it.
	h.expect(h.bash("sls deploy --stage prod"), "deny", "pipeline")
	h.expect(h.bash("vercel --prod"), "deny", "pipeline")
	h.expect(h.bash("echo ok && kubectl apply -f k8s/"), "deny", "pipeline")
	h.expect(h.bash("bash -c 'cdk deploy'"), "deny", "pipeline")
	// Destroys and writes aren't deploys: they still ask.
	h.expect(h.bash("aws s3 rm s3://bucket/old.txt"), "ask", "deletes something")
	// Reads and dry runs are fine.
	h.expect(h.bash("aws s3 ls"), "", "")
	h.expect(h.bash("kubectl apply --dry-run=client -f k8s/"), "ask", "outside your laptop")

	// No task at all: still refused.
	g := newHarness(t)
	s := &Setup{Kind: "personal", DeployPipeline: true}
	_, err := SaveSetup(g.repo, s)
	must(t, err)
	g.expect(g.bash("fly deploy"), "deny", "pipeline")
}

func TestDirectionQuestionsInNormalTasks(t *testing.T) {
	// Outside a design task, questions starting with "Direction" are
	// ordinary questions: several at once and pre-filled answers are fine.
	h := groundedTask(t, "small")
	two := map[string]any{"questions": []any{
		map[string]any{"question": "Direction of the sort?", "header": "Direction", "options": []any{map[string]any{"label": "Up"}, map[string]any{"label": "Down"}}},
		map[string]any{"question": "Direction of travel?", "header": "Direction", "options": []any{map[string]any{"label": "In"}, map[string]any{"label": "Out"}}},
	}}
	h.expect(h.pre("AskUserQuestion", two), "", "")
	two["answers"] = map[string]any{"Direction of the sort?": "Up"}
	h.expect(h.pre("AskUserQuestion", two), "", "")
}

// The setup changed from the terminal: until the user confirms it again,
// it can't switch the rules off.
func TestUnconfirmedSetupRelaxesNothing(t *testing.T) {
	h := opsTask(t, false, nil)
	h.expect(h.bash(`echo '{"kind":"personal","parts":{"devops":false}}' > .plumbline.json`), "ask", "saved project setup")
	write(t, filepath.Join(h.repo, RepoSetupFile), `{"kind":"personal","parts":{"devops":false,"design":false,"reviewer":false}}`)
	s, ok, err := LoadSetup(h.repo)
	must(t, err)
	if ok || !s.PartOn("devops") || !s.PartOn("design") || !s.PartOn("reviewer") || !s.DeployPipeline {
		t.Fatalf("an unconfirmed setup relaxed a rule: ok=%v parts=%v pipeline=%v", ok, s.Parts, s.DeployPipeline)
	}
	h.expect(h.bash("sls deploy --stage prod"), "deny", "pipeline")
	h.expect(h.bash("npm run deploy"), "deny", "pipeline")
	h.expect(h.bash("aws s3 rm s3://bucket/old.txt"), "ask", "deletes something")
}

func TestDevopsPartOff(t *testing.T) {
	h := opsTask(t, true, map[string]bool{"devops": false})
	// The phase 3 rules are off: the approved plan runs as listed...
	h.expect(h.bash("sls deploy --stage prod"), "", "")
	// ...but the core outside-the-laptop prompt stays on.
	h.expect(h.bash("vercel --prod"), "ask", "outside your laptop")
}
