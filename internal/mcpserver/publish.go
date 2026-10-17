package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jataicloud/cli/internal/api"
	"github.com/jataicloud/cli/internal/cable"
	"github.com/jataicloud/cli/internal/config"
	"github.com/jataicloud/cli/internal/deployflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Fases do jatai_publicar, no Reporter e nas notificações de progresso.
const (
	phaseCreate    = "criando o app"
	phaseProvision = "provisionando a infraestrutura"
	phasePack      = "empacotando"
	phaseUpload    = "enviando"
	phaseQueued    = "na fila"
	phaseBuild     = "construindo"
	phaseRollout   = "implantando"
)

// deployPhases traduz o status do deploy na fase mostrada.
var deployPhases = map[string]string{"queued": phaseQueued, "building": phaseBuild, "deploying": phaseRollout}

const (
	// buildLogLines é quanto do log do build pedir à API; maxLogChars, quanto
	// dele vai para o contexto da IA.
	buildLogLines = 60
	maxLogChars   = 4000
	// keptLines são as últimas linhas do build que o job guarda do WebSocket:
	// a reserva quando o log da API não vem.
	keptLines = 60
	// sideCallTimeout limita as consultas acessórias (log, falhas de processo):
	// sem elas o resultado sai mesmo assim.
	sideCallTimeout = 15 * time.Second
	// notifyTimeout limita cada notificação de progresso: cliente lento não
	// prende a resposta.
	notifyTimeout = time.Second
	// maxQueued limita as notificações à espera de envio por chamada.
	maxQueued = 32
)

const logUnavailable = "(o log do build não está disponível agora; veja o deploy no painel em https://jataicloud.com.br/apps)"

var errClosing = errors.New("O servidor MCP do Jataí está encerrando. Reinicie o servidor jatai no agente e chame " +
	"jatai_publicar de novo.")

// job é um jatai_publicar rodando em segundo plano, no contexto do servidor:
// segue depois que a chamada responde "em andamento", e o jatai_status
// (ou outro jatai_publicar) volta a esperar por ele.
type job struct {
	done chan struct{}

	mu        sync.Mutex
	slug      string
	phase     string
	url       string
	deployID  int
	status    string // final: live, failed ou partially_live
	err       error
	lines     []string
	failures  []deployflow.ProcessFailure
	logTail   string
	started   time.Time
	finished  time.Time
	listeners map[*listener]struct{}
}

// listener é uma chamada esperando o job com progressToken: recebe as
// mudanças de fase sem nunca bloquear o job (fila limitada + sinal sem espera).
type listener struct {
	wake  chan struct{}
	queue []string
}

func newJob(slug string, now time.Time) *job {
	return &job{done: make(chan struct{}), slug: slug, started: now, listeners: map[*listener]struct{}{}}
}

func (j *job) isDone() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// listen começa a ouvir o job; a fase atual, se houver, é a primeira notificação.
func (j *job) listen() *listener {
	l := &listener{wake: make(chan struct{}, 1)}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.listeners[l] = struct{}{}
	if j.phase != "" {
		j.pushLocked(l, j.phase)
	}
	return l
}

func (j *job) unlisten(l *listener) {
	j.mu.Lock()
	delete(j.listeners, l)
	j.mu.Unlock()
}

func (j *job) pushLocked(l *listener, msg string) {
	if len(l.queue) == maxQueued {
		l.queue = l.queue[1:]
	}
	l.queue = append(l.queue, msg)
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (j *job) take(l *listener) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	q := l.queue
	l.queue = nil
	return q
}

// event muda a fase e avisa quem ouve com msg (que pode detalhar a fase).
func (j *job) event(phase, msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.phase = phase
	for l := range j.listeners {
		j.pushLocked(l, msg)
	}
}

func (j *job) setPhase(phase string) {
	j.mu.Lock()
	changed := j.phase != phase
	j.mu.Unlock()
	if changed {
		j.event(phase, phase)
	}
}

func (j *job) addLine(line string) {
	j.mu.Lock()
	j.lines = append(j.lines, line)
	if len(j.lines) > keptLines {
		j.lines = j.lines[len(j.lines)-keptLines:]
	}
	building := j.phase == phaseBuild || j.phase == phaseRollout
	j.mu.Unlock()
	if !building {
		// Linha do build chegando é sinal de que ele começou.
		j.setPhase(phaseBuild)
	}
}

func (j *job) recentLines() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.lines...)
}

func (j *job) setApp(slug, url string) {
	j.mu.Lock()
	j.slug, j.url = slug, url
	j.mu.Unlock()
}

func (j *job) setDeploy(id int) {
	j.mu.Lock()
	j.deployID = id
	j.mu.Unlock()
	j.setPhase(phaseQueued)
}

func (j *job) setOutcome(failures []deployflow.ProcessFailure, logTail string) {
	j.mu.Lock()
	j.failures, j.logTail = failures, logTail
	j.mu.Unlock()
}

func (j *job) finish(status string, err error, now time.Time) {
	j.mu.Lock()
	j.status, j.err, j.finished = status, err, now
	j.mu.Unlock()
	close(j.done)
}

// jobView é uma cópia do estado do job, para montar o texto sem o lock.
type jobView struct {
	done                     bool
	slug, phase, url, status string
	deployID                 int
	err                      error
	failures                 []deployflow.ProcessFailure
	logTail                  string
	elapsed                  time.Duration
}

func (j *job) view() jobView {
	done := j.isDone()
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobView{
		done: done, slug: j.slug, phase: j.phase, url: j.url, status: j.status, deployID: j.deployID, err: j.err,
		failures: j.failures, logTail: j.logTail, elapsed: j.finished.Sub(j.started),
	}
}

// progressReporter liga o deployflow ao job: guarda a fase e as últimas linhas
// do build e avisa quem ouve. As mensagens do deployflow trazem o layout de
// terminal da gem (recuo, "\n"), aparado aqui.
type progressReporter struct {
	s   *state
	job *job
}

var _ deployflow.Reporter = (*progressReporter)(nil)

func (r *progressReporter) Info(msg string) {
	if r.s.debug() {
		fmt.Fprintln(r.s.d.Log, "jatai mcp: "+strings.TrimSpace(msg))
	}
}

// Warn (a queda do WebSocket para o polling) vai para o stderr, como na CLI.
func (r *progressReporter) Warn(msg string) {
	fmt.Fprintln(r.s.d.Log, "jatai mcp: "+strings.TrimSpace(msg))
}

// Line só perde o fim de linha: o recuo do log do build (pilha de erro,
// saída do compilador) ajuda a IA a ler o erro.
func (r *progressReporter) Line(line string) {
	line = strings.TrimRight(line, " \t\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	r.job.addLine(line)
}

// Step é o envio do pacote ("       Enviando 12KB..." → "enviando 12 KB").
func (r *progressReporter) Step(msg string) func(string) {
	text := phaseUpload
	var kb int
	if n, _ := fmt.Sscanf(strings.TrimSpace(msg), "Enviando %dKB", &kb); n == 1 {
		text = fmt.Sprintf("enviando %d KB", kb)
	}
	r.job.event(phaseUpload, text)
	return func(string) {}
}

func (r *progressReporter) Polled(status string, networkErr bool, _ time.Duration) {
	if networkErr {
		return
	}
	if phase, ok := deployPhases[status]; ok {
		r.job.setPhase(phase)
	}
}

// runner monta o fluxo de deploy com a configuração deste servidor. O token
// do WebSocket é lido agora: o jatai_login pode tê-lo trocado.
func (s *state) runner(c *api.Client, rep deployflow.Reporter) *deployflow.Runner {
	var debug io.Writer
	if s.debug() {
		debug = s.d.Log
	}
	return &deployflow.Runner{
		Client: c,
		Cable: cable.Options{
			APIURL: config.APIURL(s.d.Getenv), Token: config.Token(s.d.Paths, s.d.Getenv), UserAgent: s.d.UserAgent,
			WelcomeTimeout: s.d.WelcomeTimeout, ReconnectDelays: s.d.ReconnectDelays, Debug: debug,
		},
		Reporter:     rep,
		Now:          s.d.Now,
		Sleep:        s.d.Sleep,
		PollInterval: s.d.PollInterval,
		Git:          s.d.Git,
	}
}

// target é o que a chamada do jatai_publicar resolve antes de soltar o job.
type target struct {
	dir      string
	project  config.Project
	path     string
	slug     string
	branch   string
	explicit bool
}

func (s *state) publish(ctx context.Context, req *mcp.CallToolRequest, in publicarInput) *mcp.CallToolResult {
	deadline := time.Now().Add(s.d.Budget)
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explain(err))
	}
	t, err := s.publishTarget(ctx, req, in)
	if err != nil {
		return toolError(err.Error())
	}
	j, err := s.startOrJoin(t, c)
	if err != nil {
		return toolError(err.Error())
	}
	return s.await(ctx, req, j, deadline)
}

// publishTarget: com `app`, publica a pasta nesse app (que precisa existir);
// sem ele, o app do .jatai.yml, criando o arquivo como o jatai_criar_app.
func (s *state) publishTarget(ctx context.Context, req *mcp.CallToolRequest, in publicarInput) (target, error) {
	dir, err := s.resolveDir(ctx, req, in.Pasta)
	if err != nil {
		return target{}, err
	}
	if strings.TrimSpace(in.App) != "" {
		sl, err := s.appSlug(dir, in.App)
		if err != nil {
			return target{}, err
		}
		p, path, err := config.LoadProject(dir)
		if err != nil && !errors.Is(err, config.ErrNoProject) {
			return target{}, unreadableProject(path, err)
		}
		if p.Branch == "" {
			p.Branch = s.gitBranch(dir)
		}
		return target{dir: dir, project: p, path: path, slug: sl, branch: branchOf(p), explicit: true}, nil
	}
	p, path, err := s.ensureProject(dir, "", "")
	if err != nil {
		return target{}, err
	}
	sl, err := projectSlug(p)
	if err != nil {
		return target{}, err
	}
	return target{dir: dir, project: p, path: path, slug: sl, branch: branchOf(p)}, nil
}

// branchOf: a do projeto; HEAD destacado ou nenhuma → main.
func branchOf(p config.Project) string {
	if strings.TrimSpace(p.Branch) == "HEAD" {
		p.Branch = ""
	}
	return deployflow.BranchOf(p, "")
}

// startOrJoin devolve o job em curso para o slug ou solta um novo.
func (s *state) startOrJoin(t target, c *api.Client) (*job, error) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		return nil, errClosing
	}
	if j := s.jobs[t.slug]; j != nil && !j.isDone() {
		return j, nil
	}
	j := newJob(t.slug, s.d.Now())
	j.phase = phaseCreate
	s.jobs[t.slug] = j
	s.jobsWG.Add(1)
	go s.runPublish(j, t, c)
	return j, nil
}

func (s *state) jobFor(slug string) *job {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	return s.jobs[slug]
}

func (s *state) runPublish(j *job, t target, c *api.Client) {
	defer s.jobsWG.Done()
	var status string
	var err error
	func() {
		// Um pânico aqui derrubaria o processo inteiro (não há recover fora
		// do handler da ferramenta): vira erro do job.
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(s.d.Log, "jatai mcp: pânico no jatai_publicar: %v\n", r)
				err = errors.New("Erro interno da CLI do Jataí ao publicar. Tente de novo; se repetir, atualize a CLI.")
			}
		}()
		status, err = s.deploy(j, t, c)
	}()
	j.finish(status, err, s.d.Now())
}

// deploy é o `jatai up` em segundo plano: app → provisionamento → envio →
// acompanhamento, sempre no contexto do servidor.
func (s *state) deploy(j *job, t target, c *api.Client) (string, error) {
	ctx := s.ctx
	r := s.runner(c, &progressReporter{s: s, job: j})
	app, err := s.findApp(ctx, r, t)
	if err != nil {
		return "", err
	}
	s.adopt(j, t.slug, app)
	if app.Status != "active" && app.Status != "sleeping" {
		j.setPhase(phaseProvision)
		if err := r.Provision(ctx, app); err != nil {
			return "", err
		}
	}
	j.setPhase(phasePack)
	d, err := r.Upload(ctx, t.dir, app.Slug, t.branch)
	if err != nil {
		return "", err
	}
	j.setDeploy(d.ID)
	status, err := r.WaitDeploy(ctx, app.Slug, d.ID, deployflow.UpDeployTimeout, deployflow.UpDeployMaxPolls)
	if err != nil {
		return "", err
	}
	switch status {
	case "partially_live":
		j.setOutcome(s.processFailures(ctx, c, app.Slug, d.ID), "")
	case "failed":
		j.setOutcome(nil, s.buildTail(ctx, r, app.Slug, d.ID, j.recentLines()))
	}
	return status, nil
}

func (s *state) findApp(ctx context.Context, r *deployflow.Runner, t target) (deployflow.App, error) {
	if !t.explicit {
		return r.FindOrCreateApp(ctx, t.dir, hexBase(t.project), t.path, t.slug)
	}
	var app deployflow.App
	err := r.Client.Get(ctx, "/api/v1/apps/"+t.slug, nil, &app)
	if app.Slug == "" {
		app.Slug = t.slug
	}
	return app, err
}

// adopt guarda no job o slug verdadeiro e a URL; se o servidor desambiguou o
// slug (loja-1), o job passa a responder pelos dois.
func (s *state) adopt(j *job, guess string, app deployflow.App) {
	j.setApp(app.Slug, deployflow.AppURL(app))
	if app.Slug == guess {
		return
	}
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if cur := s.jobs[app.Slug]; cur == nil || cur.isDone() {
		s.jobs[app.Slug] = j
	}
}

func (s *state) processFailures(ctx context.Context, c *api.Client, slug string, id int) []deployflow.ProcessFailure {
	cctx, cancel := context.WithTimeout(ctx, sideCallTimeout)
	defer cancel()
	var d deployflow.Deploy
	if err := c.Get(cctx, fmt.Sprintf("/api/v1/apps/%s/deploys/%d", slug, id), nil, &d); err != nil {
		fmt.Fprintf(s.d.Log, "jatai mcp: não consegui ler as falhas do deploy #%d: %s\n", id, s.explain(err))
		return nil
	}
	return d.ProcessFailures
}

// buildTail são as últimas linhas do build, cortadas nos últimos maxLogChars
// caracteres. Sem o log da API, valem as linhas que vieram pelo WebSocket;
// sem nenhuma, o aviso de log indisponível — a resposta nunca falha por isso.
func (s *state) buildTail(ctx context.Context, r *deployflow.Runner, slug string, id int, fallback []string) string {
	lctx, cancel := context.WithTimeout(ctx, sideCallTimeout)
	defer cancel()
	lines, err := r.BuildLogTail(lctx, slug, id, buildLogLines)
	if err != nil {
		fmt.Fprintf(s.d.Log, "jatai mcp: não consegui ler o log do deploy #%d: %s\n", id, s.explain(err))
	}
	if err != nil || len(lines) == 0 {
		lines = fallback
	}
	if len(lines) == 0 {
		return logUnavailable
	}
	return lastChars(strings.Join(lines, "\n"), maxLogChars)
}

// lastChars devolve os últimos n caracteres (runas, não bytes).
func lastChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// await espera o job até o prazo da chamada, repassando as fases como
// notificações de progresso quando a chamada trouxe progressToken.
func (s *state) await(ctx context.Context, req *mcp.CallToolRequest, j *job, deadline time.Time) *mcp.CallToolResult {
	token := progressToken(req)
	var l *listener
	var wake <-chan struct{}
	if token != nil {
		l = j.listen()
		defer j.unlisten(l)
		wake = l.wake
	}
	sent := 0.0
	flush := func() {
		if l == nil {
			return
		}
		for _, msg := range j.take(l) {
			sent++
			s.notify(ctx, req, token, sent, msg)
		}
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case <-wake:
			flush()
		case <-j.done:
			flush()
			return s.jobResult(j)
		case <-timer.C:
			flush()
			return s.jobResult(j)
		case <-ctx.Done():
			return s.jobResult(j)
		}
	}
}

func progressToken(req *mcp.CallToolRequest) any {
	if req == nil || req.Params == nil || req.Session == nil {
		return nil
	}
	return req.Params.GetProgressToken()
}

func (s *state) notify(ctx context.Context, req *mcp.CallToolRequest, token any, progress float64, msg string) {
	nctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	err := req.Session.NotifyProgress(nctx, &mcp.ProgressNotificationParams{
		ProgressToken: token, Message: msg, Progress: progress,
	})
	if err != nil && s.debug() {
		fmt.Fprintf(s.d.Log, "jatai mcp: notificação de progresso não enviada: %v\n", err)
	}
}

// jobResult é o texto que a IA lê sobre o job, terminado ou não.
func (s *state) jobResult(j *job) *mcp.CallToolResult {
	v := j.view()
	switch {
	case !v.done:
		return textResult(inProgressText(v.slug, v.deployID, v.phase))
	case v.err != nil && v.deployID != 0:
		return toolError(fmt.Sprintf("O acompanhamento do deploy #%d de %s parou: %s O deploy continua no servidor; "+
			"acompanhe com jatai_status.", v.deployID, v.slug, sentence(s.explain(v.err))))
	case v.err != nil:
		return toolError(s.explain(v.err))
	case v.status == "live":
		return textResult(fmt.Sprintf("Publicado: %s\nApp %s · deploy #%d · %ds", v.url, v.slug, v.deployID,
			int(v.elapsed.Round(time.Second).Seconds())))
	case v.status == "partially_live":
		return textResult(partialText(v.url, v.failures))
	default:
		return toolError(failedText(v.slug, v.deployID, v.logTail))
	}
}

func inProgressText(slug string, id int, phase string) string {
	next := "Chame jatai_status com aguardar=true para acompanhar."
	if id == 0 {
		return fmt.Sprintf("Publicação de %s em andamento (fase: %s). %s", slug, phase, next)
	}
	return fmt.Sprintf("Deploy #%d de %s em andamento (fase: %s). %s", id, slug, phase, next)
}

func partialText(url string, failures []deployflow.ProcessFailure) string {
	lines := []string{"Publicado com ressalvas: " + url}
	for _, f := range failures {
		lines = append(lines, f.Name+": "+f.Reason)
	}
	if len(failures) == 0 {
		lines = append(lines, "Algum processo ficou fora do release; veja o deploy no painel em "+
			"https://jataicloud.com.br/apps.")
	}
	return strings.Join(lines, "\n")
}

func failedText(slug string, id int, tail string) string {
	return fmt.Sprintf("O deploy #%d de %s falhou.\n%s", id, slug, failedBlock(tail))
}

func failedBlock(tail string) string {
	return "Últimas linhas do build:\n" + tail + "\nCorrija o projeto e chame jatai_publicar de novo."
}

func (s *state) status(ctx context.Context, req *mcp.CallToolRequest, in statusInput) *mcp.CallToolResult {
	deadline := time.Now().Add(s.d.Budget)
	c, err := s.client(ctx)
	if err != nil {
		return toolError(s.explain(err))
	}
	sl, err := s.slugFor(ctx, req, in.Pasta, in.App)
	if err != nil {
		return toolError(err.Error())
	}
	wait := in.Aguardar == nil || *in.Aguardar
	if j := s.jobFor(sl); j != nil {
		if wait {
			return s.await(ctx, req, j, deadline)
		}
		return s.jobResult(j)
	}
	var app deployflow.App
	if err := c.Get(ctx, "/api/v1/apps/"+sl, nil, &app); err != nil {
		return toolError(s.explain(err))
	}
	if d := app.LastDeploy; wait && d != nil && deployPhases[d.Status] != "" {
		if res := s.waitElsewhere(ctx, c, sl, d, deadline); res != nil {
			return res
		}
		if err := c.Get(ctx, "/api/v1/apps/"+sl, nil, &app); err != nil {
			return toolError(s.explain(err))
		}
	}
	if app.Slug == "" {
		app.Slug = sl
	}
	return s.appSummary(ctx, c, app)
}

// waitElsewhere espera, até o prazo, um deploy que este processo não soltou
// (outro terminal, o painel, um servidor MCP reiniciado). nil = terminou.
func (s *state) waitElsewhere(ctx context.Context, c *api.Client, slug string, d *deployflow.Deploy,
	deadline time.Time) *mcp.CallToolResult {
	wctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	_, err := s.runner(c, nil).WaitDeploy(wctx, slug, d.ID, time.Until(deadline), deployflow.UpDeployMaxPolls)
	switch {
	case err == nil:
		return nil
	case wctx.Err() != nil && ctx.Err() == nil:
		return textResult(inProgressText(slug, d.ID, deployPhases[d.Status]))
	default:
		return toolError(s.explain(err))
	}
}

func (s *state) appSummary(ctx context.Context, c *api.Client, app deployflow.App) *mcp.CallToolResult {
	head := fmt.Sprintf("App %s: %s · %s", app.Slug, app.Status, deployflow.AppURL(app))
	d := app.LastDeploy
	if d == nil {
		return textResult(head + " · ainda sem deploy. Próximo passo: jatai_publicar.")
	}
	head += fmt.Sprintf(" · último deploy #%d: %s", d.ID, d.Status)
	if when := formatDate(d.CreatedAt); when != "" {
		head += " (" + when + ")"
	}
	if d.Status == "failed" {
		return textResult(head + "\n" + failedBlock(s.buildTail(ctx, s.runner(c, nil), app.Slug, d.ID, nil)))
	}
	return textResult(head)
}

// formatDate mostra a data no fuso em que o servidor a mandou.
func formatDate(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.Format("02/01/2006 15:04")
}
