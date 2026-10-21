package usermail

// roles are the words for the roles an invitation offers.
var roles = map[string]map[string]string{
	"en": {"owner": "owner", "admin": "administrator", "viewer": "viewer"},
	"tr": {"owner": "sahip", "admin": "yönetici", "viewer": "görüntüleyici"},
	"de": {"owner": "Inhaber", "admin": "Administrator", "viewer": "Betrachter"},
	"fr": {"owner": "propriétaire", "admin": "administrateur", "viewer": "lecteur"},
	"es": {"owner": "propietario", "admin": "administrador", "viewer": "lector"},
	"ar": {"owner": "مالك", "admin": "مسؤول", "viewer": "مشاهد"},
}

// texts[locale][kind]. A test checks that every language has every kind and that the placeholders and the number of lines agree.
var texts = map[string]map[Kind]copyText{
	"en": {
		KindVerify: {
			Subject: "Confirm your e-mail address",
			Lines:   []string{"Welcome to Quotify. Please confirm that this e-mail address is yours.", "The link works once and expires in 24 hours."},
			Button:  "Confirm e-mail address",
			Ignore:  "If you did not create a Quotify account, you can ignore this message.",
		},
		KindReset: {
			Subject: "Reset your Quotify password",
			Lines:   []string{"Somebody asked to reset the password of your Quotify account. Use the button to choose a new one.", "The link works once and expires in 1 hour. Resetting signs you out everywhere."},
			Button:  "Choose a new password",
			Ignore:  "If you did not ask for this, ignore this message: your password has not changed.",
		},
		KindInvite: {
			Subject: "You are invited to {account} on Quotify",
			Lines:   []string{"{inviter} invited you to join {account} on Quotify as {role}.", "The invitation works once and expires in 7 days."},
			Button:  "Accept the invitation",
			Ignore:  "If you do not know {inviter}, ignore this message.",
		},
		KindTaken: {
			Subject: "Somebody tried to sign up with your address",
			Lines:   []string{"Somebody tried to create a Quotify account with this e-mail address, which already has one.", "If it was you, sign in, or reset your password if you have forgotten it. Nothing has changed."},
			Button:  "",
			Ignore:  "If it was not you, you do not need to do anything.",
		},
		KindCode: {
			Subject: "Your Quotify sign-in code: {code}",
			Lines:   []string{"Use this code to sign in to Quotify: {code}", "The code works once and expires in 10 minutes. Never share it with anyone."},
			Button:  "",
			Ignore:  "If you did not ask for this code, ignore this message: nobody can sign in without it.",
		},
	},
	"tr": {
		KindVerify: {
			Subject: "E-posta adresinizi doğrulayın",
			Lines:   []string{"Quotify'a hoş geldiniz. Lütfen bu e-posta adresinin size ait olduğunu doğrulayın.", "Bağlantı yalnızca bir kez çalışır ve 24 saat sonra geçersiz olur."},
			Button:  "E-posta adresini doğrula",
			Ignore:  "Quotify hesabını siz oluşturmadıysanız bu iletiyi yok sayabilirsiniz.",
		},
		KindReset: {
			Subject: "Quotify parolanızı sıfırlayın",
			Lines:   []string{"Biri Quotify hesabınızın parolasını sıfırlamak istedi. Yeni bir parola seçmek için düğmeyi kullanın.", "Bağlantı yalnızca bir kez çalışır ve 1 saat sonra geçersiz olur. Sıfırlama, her yerdeki oturumlarınızı kapatır."},
			Button:  "Yeni parola seç",
			Ignore:  "Bunu siz istemediyseniz iletiyi yok sayın: parolanız değişmedi.",
		},
		KindInvite: {
			Subject: "Quotify'da {account} hesabına davet edildiniz",
			Lines:   []string{"{inviter}, sizi Quotify'da {account} hesabına {role} olarak katılmaya davet etti.", "Davet yalnızca bir kez kullanılabilir ve 7 gün sonra geçersiz olur."},
			Button:  "Daveti kabul et",
			Ignore:  "{inviter} kişisini tanımıyorsanız bu iletiyi yok sayın.",
		},
		KindTaken: {
			Subject: "Biri adresinizle kaydolmaya çalıştı",
			Lines:   []string{"Biri bu e-posta adresiyle bir Quotify hesabı oluşturmaya çalıştı, ancak bu adresin zaten bir hesabı var.", "Siz denediyseniz oturum açın; parolanızı unuttuysanız sıfırlayın. Hiçbir şey değişmedi."},
			Button:  "",
			Ignore:  "Siz denemediyseniz yapmanız gereken bir şey yok.",
		},
		KindCode: {
			Subject: "Quotify giriş kodunuz: {code}",
			Lines:   []string{"Quotify'a giriş yapmak için bu kodu kullanın: {code}", "Kod yalnızca bir kez çalışır ve 10 dakika sonra geçersiz olur. Kimseyle paylaşmayın."},
			Button:  "",
			Ignore:  "Bu kodu siz istemediyseniz iletiyi yok sayın: kod olmadan kimse giriş yapamaz.",
		},
	},
	"de": {
		KindVerify: {
			Subject: "Bestätigen Sie Ihre E-Mail-Adresse",
			Lines:   []string{"Willkommen bei Quotify. Bitte bestätigen Sie, dass diese E-Mail-Adresse Ihnen gehört.", "Der Link funktioniert nur einmal und läuft nach 24 Stunden ab."},
			Button:  "E-Mail-Adresse bestätigen",
			Ignore:  "Wenn Sie kein Quotify-Konto angelegt haben, können Sie diese Nachricht ignorieren.",
		},
		KindReset: {
			Subject: "Setzen Sie Ihr Quotify-Passwort zurück",
			Lines:   []string{"Jemand hat darum gebeten, das Passwort Ihres Quotify-Kontos zurückzusetzen. Wählen Sie mit der Schaltfläche ein neues.", "Der Link funktioniert nur einmal und läuft nach 1 Stunde ab. Das Zurücksetzen meldet Sie überall ab."},
			Button:  "Neues Passwort wählen",
			Ignore:  "Wenn Sie das nicht angefordert haben, ignorieren Sie diese Nachricht: Ihr Passwort hat sich nicht geändert.",
		},
		KindInvite: {
			Subject: "Einladung zu {account} bei Quotify",
			Lines:   []string{"{inviter} hat Sie eingeladen, {account} bei Quotify als {role} beizutreten.", "Die Einladung funktioniert nur einmal und läuft nach 7 Tagen ab."},
			Button:  "Einladung annehmen",
			Ignore:  "Wenn Sie {inviter} nicht kennen, ignorieren Sie diese Nachricht.",
		},
		KindTaken: {
			Subject: "Jemand hat versucht, sich mit Ihrer Adresse zu registrieren",
			Lines:   []string{"Jemand hat versucht, mit dieser E-Mail-Adresse ein Quotify-Konto anzulegen, die bereits ein Konto hat.", "Wenn Sie es waren, melden Sie sich an oder setzen Sie Ihr Passwort zurück, falls Sie es vergessen haben. Es hat sich nichts geändert."},
			Button:  "",
			Ignore:  "Wenn Sie es nicht waren, müssen Sie nichts tun.",
		},
		KindCode: {
			Subject: "Ihr Quotify-Anmeldecode: {code}",
			Lines:   []string{"Mit diesem Code melden Sie sich bei Quotify an: {code}", "Der Code funktioniert einmal und läuft nach 10 Minuten ab. Geben Sie ihn niemandem weiter."},
			Button:  "",
			Ignore:  "Wenn Sie diesen Code nicht angefordert haben, ignorieren Sie diese Nachricht: Ohne ihn kann sich niemand anmelden.",
		},
	},
	"fr": {
		KindVerify: {
			Subject: "Confirmez votre adresse e-mail",
			Lines:   []string{"Bienvenue sur Quotify. Veuillez confirmer que cette adresse e-mail est bien la vôtre.", "Le lien ne fonctionne qu'une fois et expire au bout de 24 heures."},
			Button:  "Confirmer l'adresse e-mail",
			Ignore:  "Si vous n'avez pas créé de compte Quotify, vous pouvez ignorer ce message.",
		},
		KindReset: {
			Subject: "Réinitialisez votre mot de passe Quotify",
			Lines:   []string{"Quelqu'un a demandé la réinitialisation du mot de passe de votre compte Quotify. Utilisez le bouton pour en choisir un nouveau.", "Le lien ne fonctionne qu'une fois et expire au bout d'1 heure. La réinitialisation vous déconnecte partout."},
			Button:  "Choisir un nouveau mot de passe",
			Ignore:  "Si vous n'êtes pas à l'origine de cette demande, ignorez ce message : votre mot de passe n'a pas changé.",
		},
		KindInvite: {
			Subject: "Vous êtes invité à rejoindre {account} sur Quotify",
			Lines:   []string{"{inviter} vous a invité à rejoindre {account} sur Quotify en tant que {role}.", "L'invitation ne fonctionne qu'une fois et expire au bout de 7 jours."},
			Button:  "Accepter l'invitation",
			Ignore:  "Si vous ne connaissez pas {inviter}, ignorez ce message.",
		},
		KindTaken: {
			Subject: "Quelqu'un a essayé de s'inscrire avec votre adresse",
			Lines:   []string{"Quelqu'un a essayé de créer un compte Quotify avec cette adresse e-mail, qui en a déjà un.", "Si c'était vous, connectez-vous, ou réinitialisez votre mot de passe si vous l'avez oublié. Rien n'a changé."},
			Button:  "",
			Ignore:  "Si ce n'était pas vous, vous n'avez rien à faire.",
		},
		KindCode: {
			Subject: "Votre code de connexion Quotify : {code}",
			Lines:   []string{"Utilisez ce code pour vous connecter à Quotify : {code}", "Le code ne fonctionne qu'une fois et expire au bout de 10 minutes. Ne le communiquez à personne."},
			Button:  "",
			Ignore:  "Si vous n'avez pas demandé ce code, ignorez ce message : personne ne peut se connecter sans lui.",
		},
	},
	"es": {
		KindVerify: {
			Subject: "Confirma tu dirección de correo electrónico",
			Lines:   []string{"Te damos la bienvenida a Quotify. Confirma que esta dirección de correo electrónico es tuya.", "El enlace funciona una sola vez y caduca en 24 horas."},
			Button:  "Confirmar la dirección",
			Ignore:  "Si no has creado una cuenta de Quotify, puedes ignorar este mensaje.",
		},
		KindReset: {
			Subject: "Restablece tu contraseña de Quotify",
			Lines:   []string{"Alguien ha pedido restablecer la contraseña de tu cuenta de Quotify. Usa el botón para elegir una nueva.", "El enlace funciona una sola vez y caduca en 1 hora. Al restablecerla se cierran todas tus sesiones."},
			Button:  "Elegir una contraseña nueva",
			Ignore:  "Si no lo has pedido tú, ignora este mensaje: tu contraseña no ha cambiado.",
		},
		KindInvite: {
			Subject: "Te han invitado a {account} en Quotify",
			Lines:   []string{"{inviter} te ha invitado a unirte a {account} en Quotify como {role}.", "La invitación funciona una sola vez y caduca en 7 días."},
			Button:  "Aceptar la invitación",
			Ignore:  "Si no conoces a {inviter}, ignora este mensaje.",
		},
		KindTaken: {
			Subject: "Alguien intentó registrarse con tu dirección",
			Lines:   []string{"Alguien intentó crear una cuenta de Quotify con esta dirección de correo, que ya tiene una.", "Si fuiste tú, inicia sesión o restablece tu contraseña si la has olvidado. No ha cambiado nada."},
			Button:  "",
			Ignore:  "Si no fuiste tú, no tienes que hacer nada.",
		},
		KindCode: {
			Subject: "Su código de acceso a Quotify: {code}",
			Lines:   []string{"Use este código para iniciar sesión en Quotify: {code}", "El código funciona una sola vez y caduca a los 10 minutos. No lo comparta con nadie."},
			Button:  "",
			Ignore:  "Si no solicitó este código, ignore este mensaje: nadie puede iniciar sesión sin él.",
		},
	},
	"ar": {
		KindVerify: {
			Subject: "أكّد عنوان بريدك الإلكتروني",
			Lines:   []string{"مرحبًا بك في Quotify. يرجى تأكيد أن عنوان البريد الإلكتروني هذا يخصّك.", "يعمل الرابط مرة واحدة فقط وتنتهي صلاحيته بعد 24 ساعة."},
			Button:  "تأكيد عنوان البريد",
			Ignore:  "إذا لم تنشئ حسابًا في Quotify فيمكنك تجاهل هذه الرسالة.",
		},
		KindReset: {
			Subject: "إعادة تعيين كلمة مرور Quotify",
			Lines:   []string{"طلب أحدهم إعادة تعيين كلمة مرور حسابك في Quotify. استخدم الزر لاختيار كلمة مرور جديدة.", "يعمل الرابط مرة واحدة فقط وتنتهي صلاحيته بعد ساعة واحدة. تؤدي إعادة التعيين إلى تسجيل خروجك من كل مكان."},
			Button:  "اختيار كلمة مرور جديدة",
			Ignore:  "إذا لم تطلب ذلك فتجاهل هذه الرسالة: لم تتغيّر كلمة مرورك.",
		},
		KindInvite: {
			Subject: "دُعيت للانضمام إلى {account} على Quotify",
			Lines:   []string{"دعاك {inviter} للانضمام إلى {account} على Quotify بصفة {role}.", "تعمل الدعوة مرة واحدة فقط وتنتهي صلاحيتها بعد 7 أيام."},
			Button:  "قبول الدعوة",
			Ignore:  "إذا كنت لا تعرف {inviter} فتجاهل هذه الرسالة.",
		},
		KindTaken: {
			Subject: "حاول أحدهم التسجيل بعنوانك",
			Lines:   []string{"حاول أحدهم إنشاء حساب في Quotify بعنوان البريد هذا، وهو يملك حسابًا بالفعل.", "إذا كنت أنت، فسجّل الدخول أو أعد تعيين كلمة مرورك إن نسيتها. لم يتغيّر شيء."},
			Button:  "",
			Ignore:  "إذا لم تكن أنت فلا حاجة لأي إجراء.",
		},
		KindCode: {
			Subject: "رمز دخولك إلى Quotify: {code}",
			Lines:   []string{"استخدم هذا الرمز لتسجيل الدخول إلى Quotify: {code}", "يعمل الرمز مرة واحدة فقط وتنتهي صلاحيته بعد 10 دقائق. لا تشاركه مع أحد."},
			Button:  "",
			Ignore:  "إذا لم تطلب هذا الرمز فتجاهل هذه الرسالة: لا يستطيع أحد تسجيل الدخول بدونه.",
		},
	},
}
