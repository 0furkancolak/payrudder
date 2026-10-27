package promote

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	couponscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/command"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	mailcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/command"
	mailqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/query"
	meterscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/command"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	planscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/command"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/promote/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	taxcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax/command"
	taxqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax/query"
	hookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhookendpoints/command"
	hooksqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhookendpoints/query"
)

// Wire builds the handler on the app pool or its router (the modules, each as the tenant it is asked about) and the system pool (membership, the audit row). box and
// allowPrivate are the webhook module's: without a box, webhook endpoints cannot be made and the promotion reports that on their changes.
func Wire(appDB pgxx.Beginner, systemPool *pgxpool.Pool, box *secretbox.Box, allowPrivate bool, log *slog.Logger) *Handler {
	db := store.New(appDB)
	m := command.Modules{
		MetersQ: metersqry.New(db), MetersC: meterscmd.New(db),
		PlansQ: plansqry.New(db), PlansC: planscmd.New(db),
		TaxQ: taxqry.New(db), TaxC: taxcmd.New(db),
		CouponsQ: couponsqry.New(db), CouponsC: couponscmd.New(db, time.Now),
		MailQ: mailqry.New(db), MailC: mailcmd.New(db),
		HooksQ: hooksqry.New(db), HooksC: hookscmd.New(db, time.Now, box, allowPrivate),
	}
	return New(command.New(systemPool, m, log))
}
