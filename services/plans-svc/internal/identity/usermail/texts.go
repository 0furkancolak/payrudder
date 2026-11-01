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

// ticketWords are the words a ticket mail names a status and a priority with, per language.
var ticketWords = map[string]map[string]string{
	"en": {"open": "waiting for support", "pending": "waiting for you", "solved": "solved", "closed": "closed", "low": "low", "normal": "normal", "high": "high", "urgent": "urgent"},
	"tr": {"open": "destek ekibini bekliyor", "pending": "yanıtınızı bekliyor", "solved": "çözüldü", "closed": "kapatıldı", "low": "düşük", "normal": "normal", "high": "yüksek", "urgent": "acil"},
	"de": {"open": "wartet auf den Support", "pending": "wartet auf Ihre Antwort", "solved": "gelöst", "closed": "geschlossen", "low": "niedrig", "normal": "normal", "high": "hoch", "urgent": "dringend"},
	"fr": {"open": "en attente du support", "pending": "en attente de votre réponse", "solved": "résolu", "closed": "fermé", "low": "faible", "normal": "normale", "high": "élevée", "urgent": "urgente"},
	"es": {"open": "esperando al soporte", "pending": "esperando tu respuesta", "solved": "resuelto", "closed": "cerrado", "low": "baja", "normal": "normal", "high": "alta", "urgent": "urgente"},
	"ar": {"open": "بانتظار الدعم", "pending": "بانتظار ردّك", "solved": "محلولة", "closed": "مغلقة", "low": "منخفضة", "normal": "عادية", "high": "عالية", "urgent": "عاجلة"},
}

// texts[locale][kind]. A test checks that every language has every kind and that the placeholders and the number of lines agree.
var texts = map[string]map[Kind]copyText{
	"en": {
		KindVerify: {
			Subject: "Confirm your e-mail address",
			Lines:   []string{"Welcome to Payrudder. Please confirm that this e-mail address is yours.", "The link works once and expires in 24 hours."},
			Button:  "Confirm e-mail address",
			Ignore:  "If you did not create a Payrudder account, you can ignore this message.",
		},
		KindReset: {
			Subject: "Reset your Payrudder password",
			Lines:   []string{"Somebody asked to reset the password of your Payrudder account. Use the button to choose a new one.", "The link works once and expires in 1 hour. Resetting signs you out everywhere."},
			Button:  "Choose a new password",
			Ignore:  "If you did not ask for this, ignore this message: your password has not changed.",
		},
		KindInvite: {
			Subject: "You are invited to {account} on Payrudder",
			Lines:   []string{"{inviter} invited you to join {account} on Payrudder as {role}.", "The invitation works once and expires in 7 days."},
			Button:  "Accept the invitation",
			Ignore:  "If you do not know {inviter}, ignore this message.",
		},
		KindTaken: {
			Subject: "Somebody tried to sign up with your address",
			Lines:   []string{"Somebody tried to create a Payrudder account with this e-mail address, which already has one.", "If it was you, sign in, or reset your password if you have forgotten it. Nothing has changed."},
			Button:  "",
			Ignore:  "If it was not you, you do not need to do anything.",
		},
		KindCode: {
			Subject: "Your Payrudder sign-in code: {code}",
			Lines:   []string{"Use this code to sign in to Payrudder: {code}", "The code works once and expires in 10 minutes. Never share it with anyone."},
			Button:  "",
			Ignore:  "If you did not ask for this code, ignore this message: nobody can sign in without it.",
		},
		KindTicketNew: {
			Subject: "New support ticket {ticket}: {subject}",
			Lines:   []string{"{account} opened ticket {ticket} ({priority} priority): {subject}", "Open the ticket in the operator console to answer it."},
			Button:  "Open the ticket",
			Ignore:  "You get this because your address is listed as a Payrudder operator.",
		},
		KindTicketReply: {
			Subject: "Payrudder support answered ticket {ticket}",
			Lines:   []string{"Payrudder support answered your ticket {ticket}: {subject}", "Open the ticket to read the answer and reply."},
			Button:  "Read the answer",
			Ignore:  "You get this because you opened the ticket. Replies to this e-mail are not read: write in the ticket.",
		},
		KindTicketStatus: {
			Subject: "Your ticket {ticket} is now: {status}",
			Lines:   []string{"The status of your ticket {ticket} ({subject}) changed to: {status}.", "Open the ticket to see the details."},
			Button:  "Open the ticket",
			Ignore:  "You get this because you opened the ticket. Replies to this e-mail are not read: write in the ticket.",
		},
	},
	"tr": {
		KindVerify: {
			Subject: "E-posta adresinizi doğrulayın",
			Lines:   []string{"Payrudder'a hoş geldiniz. Lütfen bu e-posta adresinin size ait olduğunu doğrulayın.", "Bağlantı yalnızca bir kez çalışır ve 24 saat sonra geçersiz olur."},
			Button:  "E-posta adresini doğrula",
			Ignore:  "Payrudder hesabını siz oluşturmadıysanız bu iletiyi yok sayabilirsiniz.",
		},
		KindReset: {
			Subject: "Payrudder parolanızı sıfırlayın",
			Lines:   []string{"Biri Payrudder hesabınızın parolasını sıfırlamak istedi. Yeni bir parola seçmek için düğmeyi kullanın.", "Bağlantı yalnızca bir kez çalışır ve 1 saat sonra geçersiz olur. Sıfırlama, her yerdeki oturumlarınızı kapatır."},
			Button:  "Yeni parola seç",
			Ignore:  "Bunu siz istemediyseniz iletiyi yok sayın: parolanız değişmedi.",
		},
		KindInvite: {
			Subject: "Payrudder'da {account} hesabına davet edildiniz",
			Lines:   []string{"{inviter}, sizi Payrudder'da {account} hesabına {role} olarak katılmaya davet etti.", "Davet yalnızca bir kez kullanılabilir ve 7 gün sonra geçersiz olur."},
			Button:  "Daveti kabul et",
			Ignore:  "{inviter} kişisini tanımıyorsanız bu iletiyi yok sayın.",
		},
		KindTaken: {
			Subject: "Biri adresinizle kaydolmaya çalıştı",
			Lines:   []string{"Biri bu e-posta adresiyle bir Payrudder hesabı oluşturmaya çalıştı, ancak bu adresin zaten bir hesabı var.", "Siz denediyseniz oturum açın; parolanızı unuttuysanız sıfırlayın. Hiçbir şey değişmedi."},
			Button:  "",
			Ignore:  "Siz denemediyseniz yapmanız gereken bir şey yok.",
		},
		KindCode: {
			Subject: "Payrudder giriş kodunuz: {code}",
			Lines:   []string{"Payrudder'a giriş yapmak için bu kodu kullanın: {code}", "Kod yalnızca bir kez çalışır ve 10 dakika sonra geçersiz olur. Kimseyle paylaşmayın."},
			Button:  "",
			Ignore:  "Bu kodu siz istemediyseniz iletiyi yok sayın: kod olmadan kimse giriş yapamaz.",
		},
		KindTicketNew: {
			Subject: "Yeni destek talebi {ticket}: {subject}",
			Lines:   []string{"{account}, {priority} öncelikli {ticket} numaralı talebi açtı: {subject}", "Yanıtlamak için talebi operatör konsolunda açın."},
			Button:  "Talebi aç",
			Ignore:  "Adresiniz Payrudder operatörü olarak listelendiği için bu iletiyi alıyorsunuz.",
		},
		KindTicketReply: {
			Subject: "Payrudder destek ekibi {ticket} numaralı talebinizi yanıtladı",
			Lines:   []string{"Payrudder destek ekibi {ticket} numaralı talebinizi yanıtladı: {subject}", "Yanıtı okumak ve cevap yazmak için talebi açın."},
			Button:  "Yanıtı oku",
			Ignore:  "Talebi siz açtığınız için bu iletiyi alıyorsunuz. Bu e-postaya verilen yanıtlar okunmaz: yanıtınızı talebe yazın.",
		},
		KindTicketStatus: {
			Subject: "{ticket} numaralı talebinizin durumu: {status}",
			Lines:   []string{"{ticket} numaralı talebinizin ({subject}) durumu değişti: {status}.", "Ayrıntıları görmek için talebi açın."},
			Button:  "Talebi aç",
			Ignore:  "Talebi siz açtığınız için bu iletiyi alıyorsunuz. Bu e-postaya verilen yanıtlar okunmaz: yanıtınızı talebe yazın.",
		},
	},
	"de": {
		KindVerify: {
			Subject: "Bestätigen Sie Ihre E-Mail-Adresse",
			Lines:   []string{"Willkommen bei Payrudder. Bitte bestätigen Sie, dass diese E-Mail-Adresse Ihnen gehört.", "Der Link funktioniert nur einmal und läuft nach 24 Stunden ab."},
			Button:  "E-Mail-Adresse bestätigen",
			Ignore:  "Wenn Sie kein Payrudder-Konto angelegt haben, können Sie diese Nachricht ignorieren.",
		},
		KindReset: {
			Subject: "Setzen Sie Ihr Payrudder-Passwort zurück",
			Lines:   []string{"Jemand hat darum gebeten, das Passwort Ihres Payrudder-Kontos zurückzusetzen. Wählen Sie mit der Schaltfläche ein neues.", "Der Link funktioniert nur einmal und läuft nach 1 Stunde ab. Das Zurücksetzen meldet Sie überall ab."},
			Button:  "Neues Passwort wählen",
			Ignore:  "Wenn Sie das nicht angefordert haben, ignorieren Sie diese Nachricht: Ihr Passwort hat sich nicht geändert.",
		},
		KindInvite: {
			Subject: "Einladung zu {account} bei Payrudder",
			Lines:   []string{"{inviter} hat Sie eingeladen, {account} bei Payrudder als {role} beizutreten.", "Die Einladung funktioniert nur einmal und läuft nach 7 Tagen ab."},
			Button:  "Einladung annehmen",
			Ignore:  "Wenn Sie {inviter} nicht kennen, ignorieren Sie diese Nachricht.",
		},
		KindTaken: {
			Subject: "Jemand hat versucht, sich mit Ihrer Adresse zu registrieren",
			Lines:   []string{"Jemand hat versucht, mit dieser E-Mail-Adresse ein Payrudder-Konto anzulegen, die bereits ein Konto hat.", "Wenn Sie es waren, melden Sie sich an oder setzen Sie Ihr Passwort zurück, falls Sie es vergessen haben. Es hat sich nichts geändert."},
			Button:  "",
			Ignore:  "Wenn Sie es nicht waren, müssen Sie nichts tun.",
		},
		KindCode: {
			Subject: "Ihr Payrudder-Anmeldecode: {code}",
			Lines:   []string{"Mit diesem Code melden Sie sich bei Payrudder an: {code}", "Der Code funktioniert einmal und läuft nach 10 Minuten ab. Geben Sie ihn niemandem weiter."},
			Button:  "",
			Ignore:  "Wenn Sie diesen Code nicht angefordert haben, ignorieren Sie diese Nachricht: Ohne ihn kann sich niemand anmelden.",
		},
		KindTicketNew: {
			Subject: "Neues Support-Ticket {ticket}: {subject}",
			Lines:   []string{"{account} hat das Ticket {ticket} eröffnet (Priorität {priority}): {subject}", "Öffnen Sie das Ticket in der Operator-Konsole, um zu antworten."},
			Button:  "Ticket öffnen",
			Ignore:  "Sie erhalten diese Nachricht, weil Ihre Adresse als Payrudder-Operator eingetragen ist.",
		},
		KindTicketReply: {
			Subject: "Der Payrudder-Support hat auf Ticket {ticket} geantwortet",
			Lines:   []string{"Der Payrudder-Support hat auf Ihr Ticket {ticket} geantwortet: {subject}", "Öffnen Sie das Ticket, um die Antwort zu lesen und zu antworten."},
			Button:  "Antwort lesen",
			Ignore:  "Sie erhalten diese Nachricht, weil Sie das Ticket eröffnet haben. Antworten auf diese E-Mail werden nicht gelesen: schreiben Sie im Ticket.",
		},
		KindTicketStatus: {
			Subject: "Ihr Ticket {ticket} ist jetzt: {status}",
			Lines:   []string{"Der Status Ihres Tickets {ticket} ({subject}) hat sich geändert: {status}.", "Öffnen Sie das Ticket, um die Details zu sehen."},
			Button:  "Ticket öffnen",
			Ignore:  "Sie erhalten diese Nachricht, weil Sie das Ticket eröffnet haben. Antworten auf diese E-Mail werden nicht gelesen: schreiben Sie im Ticket.",
		},
	},
	"fr": {
		KindVerify: {
			Subject: "Confirmez votre adresse e-mail",
			Lines:   []string{"Bienvenue sur Payrudder. Veuillez confirmer que cette adresse e-mail est bien la vôtre.", "Le lien ne fonctionne qu'une fois et expire au bout de 24 heures."},
			Button:  "Confirmer l'adresse e-mail",
			Ignore:  "Si vous n'avez pas créé de compte Payrudder, vous pouvez ignorer ce message.",
		},
		KindReset: {
			Subject: "Réinitialisez votre mot de passe Payrudder",
			Lines:   []string{"Quelqu'un a demandé la réinitialisation du mot de passe de votre compte Payrudder. Utilisez le bouton pour en choisir un nouveau.", "Le lien ne fonctionne qu'une fois et expire au bout d'1 heure. La réinitialisation vous déconnecte partout."},
			Button:  "Choisir un nouveau mot de passe",
			Ignore:  "Si vous n'êtes pas à l'origine de cette demande, ignorez ce message : votre mot de passe n'a pas changé.",
		},
		KindInvite: {
			Subject: "Vous êtes invité à rejoindre {account} sur Payrudder",
			Lines:   []string{"{inviter} vous a invité à rejoindre {account} sur Payrudder en tant que {role}.", "L'invitation ne fonctionne qu'une fois et expire au bout de 7 jours."},
			Button:  "Accepter l'invitation",
			Ignore:  "Si vous ne connaissez pas {inviter}, ignorez ce message.",
		},
		KindTaken: {
			Subject: "Quelqu'un a essayé de s'inscrire avec votre adresse",
			Lines:   []string{"Quelqu'un a essayé de créer un compte Payrudder avec cette adresse e-mail, qui en a déjà un.", "Si c'était vous, connectez-vous, ou réinitialisez votre mot de passe si vous l'avez oublié. Rien n'a changé."},
			Button:  "",
			Ignore:  "Si ce n'était pas vous, vous n'avez rien à faire.",
		},
		KindCode: {
			Subject: "Votre code de connexion Payrudder : {code}",
			Lines:   []string{"Utilisez ce code pour vous connecter à Payrudder : {code}", "Le code ne fonctionne qu'une fois et expire au bout de 10 minutes. Ne le communiquez à personne."},
			Button:  "",
			Ignore:  "Si vous n'avez pas demandé ce code, ignorez ce message : personne ne peut se connecter sans lui.",
		},
		KindTicketNew: {
			Subject: "Nouveau ticket de support {ticket} : {subject}",
			Lines:   []string{"{account} a ouvert le ticket {ticket} (priorité {priority}) : {subject}", "Ouvrez le ticket dans la console opérateur pour y répondre."},
			Button:  "Ouvrir le ticket",
			Ignore:  "Vous recevez ce message parce que votre adresse figure parmi les opérateurs de Payrudder.",
		},
		KindTicketReply: {
			Subject: "Le support Payrudder a répondu au ticket {ticket}",
			Lines:   []string{"Le support Payrudder a répondu à votre ticket {ticket} : {subject}", "Ouvrez le ticket pour lire la réponse et répondre."},
			Button:  "Lire la réponse",
			Ignore:  "Vous recevez ce message parce que vous avez ouvert le ticket. Les réponses à cet e-mail ne sont pas lues : écrivez dans le ticket.",
		},
		KindTicketStatus: {
			Subject: "Votre ticket {ticket} est maintenant : {status}",
			Lines:   []string{"Le statut de votre ticket {ticket} ({subject}) a changé : {status}.", "Ouvrez le ticket pour voir les détails."},
			Button:  "Ouvrir le ticket",
			Ignore:  "Vous recevez ce message parce que vous avez ouvert le ticket. Les réponses à cet e-mail ne sont pas lues : écrivez dans le ticket.",
		},
	},
	"es": {
		KindVerify: {
			Subject: "Confirma tu dirección de correo electrónico",
			Lines:   []string{"Te damos la bienvenida a Payrudder. Confirma que esta dirección de correo electrónico es tuya.", "El enlace funciona una sola vez y caduca en 24 horas."},
			Button:  "Confirmar la dirección",
			Ignore:  "Si no has creado una cuenta de Payrudder, puedes ignorar este mensaje.",
		},
		KindReset: {
			Subject: "Restablece tu contraseña de Payrudder",
			Lines:   []string{"Alguien ha pedido restablecer la contraseña de tu cuenta de Payrudder. Usa el botón para elegir una nueva.", "El enlace funciona una sola vez y caduca en 1 hora. Al restablecerla se cierran todas tus sesiones."},
			Button:  "Elegir una contraseña nueva",
			Ignore:  "Si no lo has pedido tú, ignora este mensaje: tu contraseña no ha cambiado.",
		},
		KindInvite: {
			Subject: "Te han invitado a {account} en Payrudder",
			Lines:   []string{"{inviter} te ha invitado a unirte a {account} en Payrudder como {role}.", "La invitación funciona una sola vez y caduca en 7 días."},
			Button:  "Aceptar la invitación",
			Ignore:  "Si no conoces a {inviter}, ignora este mensaje.",
		},
		KindTaken: {
			Subject: "Alguien intentó registrarse con tu dirección",
			Lines:   []string{"Alguien intentó crear una cuenta de Payrudder con esta dirección de correo, que ya tiene una.", "Si fuiste tú, inicia sesión o restablece tu contraseña si la has olvidado. No ha cambiado nada."},
			Button:  "",
			Ignore:  "Si no fuiste tú, no tienes que hacer nada.",
		},
		KindCode: {
			Subject: "Su código de acceso a Payrudder: {code}",
			Lines:   []string{"Use este código para iniciar sesión en Payrudder: {code}", "El código funciona una sola vez y caduca a los 10 minutos. No lo comparta con nadie."},
			Button:  "",
			Ignore:  "Si no solicitó este código, ignore este mensaje: nadie puede iniciar sesión sin él.",
		},
		KindTicketNew: {
			Subject: "Nuevo ticket de soporte {ticket}: {subject}",
			Lines:   []string{"{account} abrió el ticket {ticket} (prioridad {priority}): {subject}", "Abre el ticket en la consola de operadores para responder."},
			Button:  "Abrir el ticket",
			Ignore:  "Recibes este mensaje porque tu dirección figura como operador de Payrudder.",
		},
		KindTicketReply: {
			Subject: "El soporte de Payrudder respondió al ticket {ticket}",
			Lines:   []string{"El soporte de Payrudder respondió a tu ticket {ticket}: {subject}", "Abre el ticket para leer la respuesta y contestar."},
			Button:  "Leer la respuesta",
			Ignore:  "Recibes este mensaje porque abriste el ticket. Las respuestas a este correo no se leen: escribe en el ticket.",
		},
		KindTicketStatus: {
			Subject: "Tu ticket {ticket} ahora está: {status}",
			Lines:   []string{"El estado de tu ticket {ticket} ({subject}) cambió: {status}.", "Abre el ticket para ver los detalles."},
			Button:  "Abrir el ticket",
			Ignore:  "Recibes este mensaje porque abriste el ticket. Las respuestas a este correo no se leen: escribe en el ticket.",
		},
	},
	"ar": {
		KindVerify: {
			Subject: "أكّد عنوان بريدك الإلكتروني",
			Lines:   []string{"مرحبًا بك في Payrudder. يرجى تأكيد أن عنوان البريد الإلكتروني هذا يخصّك.", "يعمل الرابط مرة واحدة فقط وتنتهي صلاحيته بعد 24 ساعة."},
			Button:  "تأكيد عنوان البريد",
			Ignore:  "إذا لم تنشئ حسابًا في Payrudder فيمكنك تجاهل هذه الرسالة.",
		},
		KindReset: {
			Subject: "إعادة تعيين كلمة مرور Payrudder",
			Lines:   []string{"طلب أحدهم إعادة تعيين كلمة مرور حسابك في Payrudder. استخدم الزر لاختيار كلمة مرور جديدة.", "يعمل الرابط مرة واحدة فقط وتنتهي صلاحيته بعد ساعة واحدة. تؤدي إعادة التعيين إلى تسجيل خروجك من كل مكان."},
			Button:  "اختيار كلمة مرور جديدة",
			Ignore:  "إذا لم تطلب ذلك فتجاهل هذه الرسالة: لم تتغيّر كلمة مرورك.",
		},
		KindInvite: {
			Subject: "دُعيت للانضمام إلى {account} على Payrudder",
			Lines:   []string{"دعاك {inviter} للانضمام إلى {account} على Payrudder بصفة {role}.", "تعمل الدعوة مرة واحدة فقط وتنتهي صلاحيتها بعد 7 أيام."},
			Button:  "قبول الدعوة",
			Ignore:  "إذا كنت لا تعرف {inviter} فتجاهل هذه الرسالة.",
		},
		KindTaken: {
			Subject: "حاول أحدهم التسجيل بعنوانك",
			Lines:   []string{"حاول أحدهم إنشاء حساب في Payrudder بعنوان البريد هذا، وهو يملك حسابًا بالفعل.", "إذا كنت أنت، فسجّل الدخول أو أعد تعيين كلمة مرورك إن نسيتها. لم يتغيّر شيء."},
			Button:  "",
			Ignore:  "إذا لم تكن أنت فلا حاجة لأي إجراء.",
		},
		KindCode: {
			Subject: "رمز دخولك إلى Payrudder: {code}",
			Lines:   []string{"استخدم هذا الرمز لتسجيل الدخول إلى Payrudder: {code}", "يعمل الرمز مرة واحدة فقط وتنتهي صلاحيته بعد 10 دقائق. لا تشاركه مع أحد."},
			Button:  "",
			Ignore:  "إذا لم تطلب هذا الرمز فتجاهل هذه الرسالة: لا يستطيع أحد تسجيل الدخول بدونه.",
		},
		KindTicketNew: {
			Subject: "تذكرة دعم جديدة {ticket}: {subject}",
			Lines:   []string{"فتحت {account} التذكرة {ticket} (أولوية {priority}): {subject}", "افتح التذكرة في لوحة المشغّلين للرد عليها."},
			Button:  "فتح التذكرة",
			Ignore:  "تصلك هذه الرسالة لأن عنوانك مدرج ضمن مشغّلي Payrudder.",
		},
		KindTicketReply: {
			Subject: "ردّ دعم Payrudder على التذكرة {ticket}",
			Lines:   []string{"ردّ دعم Payrudder على تذكرتك {ticket}: {subject}", "افتح التذكرة لقراءة الردّ والإجابة."},
			Button:  "قراءة الردّ",
			Ignore:  "تصلك هذه الرسالة لأنك فتحت التذكرة. لا تُقرأ الردود على هذه الرسالة الإلكترونية: اكتب في التذكرة.",
		},
		KindTicketStatus: {
			Subject: "حالة تذكرتك {ticket} الآن: {status}",
			Lines:   []string{"تغيّرت حالة تذكرتك {ticket} ({subject}): {status}.", "افتح التذكرة لعرض التفاصيل."},
			Button:  "فتح التذكرة",
			Ignore:  "تصلك هذه الرسالة لأنك فتحت التذكرة. لا تُقرأ الردود على هذه الرسالة الإلكترونية: اكتب في التذكرة.",
		},
	},
}
