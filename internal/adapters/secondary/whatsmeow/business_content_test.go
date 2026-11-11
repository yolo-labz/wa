package whatsmeow

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// The fixtures below copy the wire shapes a Cloud-API recruiting bot
// actually sends (h151: six "entrevista digital" invites stored body="")
// with invented text — the real payloads carry a third party's data.

const (
	inviteTitle = "Entrevista digital"
	inviteText  = "Olá, *Ana*! Seu perfil avançou para a entrevista digital."
)

// hydratedInvite is the dominant shape: templateMessage.hydratedTemplate
// (the plain field, not the Format oneof) with a URL CTA and two quick
// replies.
func hydratedInvite() *waE2E.TemplateMessage_HydratedFourRowTemplate {
	return &waE2E.TemplateMessage_HydratedFourRowTemplate{
		Title:               &waE2E.TemplateMessage_HydratedFourRowTemplate_HydratedTitleText{HydratedTitleText: inviteTitle},
		HydratedContentText: proto.String(inviteText),
		HydratedButtons: []*waE2E.HydratedTemplateButton{
			{HydratedButton: &waE2E.HydratedTemplateButton_UrlButton{UrlButton: &waE2E.HydratedTemplateButton_HydratedURLButton{
				DisplayText: proto.String("Iniciar Entrevista"), URL: proto.String("https://w.example/s/abc"),
			}}},
			{HydratedButton: &waE2E.HydratedTemplateButton_QuickReplyButton{QuickReplyButton: &waE2E.HydratedTemplateButton_HydratedQuickReplyButton{
				DisplayText: proto.String("Dificuldade na fala (PCD)"), ID: proto.String("pcd"),
			}}},
			{HydratedButton: &waE2E.HydratedTemplateButton_CallButton{CallButton: &waE2E.HydratedTemplateButton_HydratedCallButton{
				DisplayText: proto.String("Ligar"), PhoneNumber: proto.String("+5511900000000"),
			}}},
		},
	}
}

const hydratedInviteBody = inviteTitle + "\n" + inviteText + "\n" +
	"[button] Iniciar Entrevista → https://w.example/s/abc\n" +
	"[button] Dificuldade na fala (PCD)\n" +
	"[button] Ligar → tel:+5511900000000"

// nativeFlowInvite is the other observed shape:
// templateMessage.interactiveMessageTemplate with nativeFlow buttons whose
// text lives in buttonParamsJSON.
func nativeFlowInvite() *waE2E.InteractiveMessage {
	return &waE2E.InteractiveMessage{
		Header: &waE2E.InteractiveMessage_Header{Title: proto.String("")},
		Body:   &waE2E.InteractiveMessage_Body{Text: proto.String(inviteText)},
		Footer: &waE2E.InteractiveMessage_Footer{Text: proto.String("ajuda@example.com")},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
			Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
				{Name: proto.String("cta_url"), ButtonParamsJSON: proto.String(`{"display_text":"Iniciar Entrevista","url":"https:\/\/x.example\/red\/1"}`)},
				{Name: proto.String("quick_reply"), ButtonParamsJSON: proto.String(`{"display_text":"Bloquear n\u00famero","id":"block"}`)},
				{Name: proto.String("cta_call"), ButtonParamsJSON: proto.String(`{"display_text":"Ligar","phone_number":"+5511900000000"}`)},
			},
		}},
	}
}

const nativeFlowInviteBody = inviteText + "\najuda@example.com\n" +
	"[button] Iniciar Entrevista → https://x.example/red/1\n" +
	"[button] Bloquear número\n" +
	"[button] Ligar → tel:+5511900000000"

func TestMessageContentBusinessVariants(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{
			name: "template hydratedTemplate field",
			msg:  &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{HydratedTemplate: hydratedInvite()}},
			want: hydratedInviteBody,
		},
		{
			name: "template hydratedFourRowTemplate oneof",
			msg: &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
				Format: &waE2E.TemplateMessage_HydratedFourRowTemplate_{HydratedFourRowTemplate: hydratedInvite()},
			}},
			want: hydratedInviteBody,
		},
		{
			name: "template interactiveMessageTemplate with native flow",
			msg: &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
				Format: &waE2E.TemplateMessage_InteractiveMessageTemplate{InteractiveMessageTemplate: nativeFlowInvite()},
			}},
			want: nativeFlowInviteBody,
		},
		{
			name: "template fourRowTemplate reads its hydrated HSM",
			msg: &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
				Format: &waE2E.TemplateMessage_FourRowTemplate_{FourRowTemplate: &waE2E.TemplateMessage_FourRowTemplate{
					Content: &waE2E.HighlyStructuredMessage{
						Namespace:   proto.String("ns"),
						HydratedHsm: &waE2E.TemplateMessage{HydratedTemplate: hydratedInvite()},
					},
					Footer: &waE2E.HighlyStructuredMessage{Namespace: proto.String("ns"), ElementName: proto.String("unhydrated")},
				}},
			}},
			want: hydratedInviteBody,
		},
		{
			name: "interactive top level",
			msg:  &waE2E.Message{InteractiveMessage: nativeFlowInvite()},
			want: nativeFlowInviteBody,
		},
		{
			name: "interactive single_select menu",
			msg: &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
				Body: &waE2E.InteractiveMessage_Body{Text: proto.String("Escolha um horário")},
				InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
					Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{{
						Name:             proto.String("single_select"),
						ButtonParamsJSON: proto.String(`{"title":"Horários","sections":[{"title":"Segunda","rows":[{"title":"10:00","description":"online"},{"title":"14:00"}]}]}`),
					}},
				}},
			}},
			want: "Escolha um horário\n[button] Horários\nSegunda\n[list] 10:00 — online\n[list] 14:00",
		},
		{
			name: "interactive carousel renders every card",
			msg: &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
				Body: &waE2E.InteractiveMessage_Body{Text: proto.String("Vagas")},
				InteractiveMessage: &waE2E.InteractiveMessage_CarouselMessage_{CarouselMessage: &waE2E.InteractiveMessage_CarouselMessage{
					Cards: []*waE2E.InteractiveMessage{
						{Body: &waE2E.InteractiveMessage_Body{Text: proto.String("Vaga A")}},
						{Body: &waE2E.InteractiveMessage_Body{Text: proto.String("Vaga B")}},
					},
				}},
			}},
			want: "Vagas\nVaga A\nVaga B",
		},
		{
			name: "interactive unparseable button still shows its kind",
			msg: &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
				Body: &waE2E.InteractiveMessage_Body{Text: proto.String("x")},
				InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
					Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{{Name: proto.String("cta_url"), ButtonParamsJSON: proto.String("{")}},
				}},
			}},
			want: "x\n[button] cta_url",
		},
		{
			name: "buttons message",
			msg: &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{
				Header:      &waE2E.ButtonsMessage_Text{Text: "Processo seletivo"},
				ContentText: proto.String("Você quer participar?"),
				FooterText:  proto.String("Responda até sexta"),
				Buttons: []*waE2E.ButtonsMessage_Button{
					{ButtonID: proto.String("1"), ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: proto.String("Quero participar")}},
					{ButtonID: proto.String("2"), ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{DisplayText: proto.String("Não tenho interesse")}},
				},
			}},
			want: "Processo seletivo\nVocê quer participar?\nResponda até sexta\n[button] Quero participar\n[button] Não tenho interesse",
		},
		{
			name: "list message",
			msg: &waE2E.Message{ListMessage: &waE2E.ListMessage{
				Title:       proto.String("Agenda"),
				Description: proto.String("Escolha um horário"),
				ButtonText:  proto.String("Ver horários"),
				Sections: []*waE2E.ListMessage_Section{{
					Title: proto.String("Terça"),
					Rows: []*waE2E.ListMessage_Row{
						{Title: proto.String("09:00"), Description: proto.String("remoto"), RowID: proto.String("a")},
						{Title: proto.String("15:00"), RowID: proto.String("b")},
					},
				}},
			}},
			want: "Agenda\nEscolha um horário\n[button] Ver horários\nTerça\n[list] 09:00 — remoto\n[list] 15:00",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, mediaType, caption := messageContent(tc.msg)
			if body != tc.want || mediaType != "" || caption != "" {
				t.Fatalf("messageContent =\n(%q, %q, %q)\nwant body\n%q", body, mediaType, caption, tc.want)
			}
		})
	}
}

func TestMessageContentUnwrapsContainers(t *testing.T) {
	template := &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{HydratedTemplate: hydratedInvite()}}
	nested := &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{
		Message: &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: template}},
		}},
	}}
	if body, _, _ := messageContent(nested); body != hydratedInviteBody {
		t.Fatalf("nested wrappers: body = %q", body)
	}

	doc := &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
		Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			Caption: proto.String("vaga.pdf"), Mimetype: proto.String("application/pdf"),
		}},
	}}
	if body, mt, capt := messageContent(doc); body != "vaga.pdf" || mt != "application/pdf" || capt != "vaga.pdf" {
		t.Fatalf("documentWithCaption = (%q, %q, %q)", body, mt, capt)
	}

	for _, wrap := range []*waE2E.Message{
		{ViewOnceMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("oi")}}},
		{ViewOnceMessageV2Extension: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("oi")}}},
		{GroupMentionedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("oi")}}},
		{BotInvokeMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: proto.String("oi")}}},
	} {
		if body, _, _ := messageContent(wrap); body != "oi" {
			t.Fatalf("wrapper %v: body = %q", wrap, body)
		}
	}
}

// A self-similar envelope deeper than any real one must terminate.
func TestUnwrapMessageIsBounded(t *testing.T) {
	m := &waE2E.Message{Conversation: proto.String("core")}
	for range maxUnwrapDepth * 4 {
		m = &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: m}}
	}
	if body, _, _ := messageContent(m); body != "" {
		t.Fatalf("over-deep envelope rendered %q; the cap should stop before the core", body)
	}
}

func TestContentFromRaw(t *testing.T) {
	raw, err := proto.Marshal(&waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{HydratedTemplate: hydratedInvite()}})
	if err != nil {
		t.Fatal(err)
	}
	if body, _, _ := ContentFromRaw(raw); body != hydratedInviteBody {
		t.Fatalf("ContentFromRaw body = %q", body)
	}
	for _, bad := range [][]byte{nil, {}, {0xff, 0xff, 0xff}} {
		if body, mt, capt := ContentFromRaw(bad); body+mt+capt != "" {
			t.Fatalf("ContentFromRaw(%x) = (%q, %q, %q), want empty", bad, body, mt, capt)
		}
	}
}
