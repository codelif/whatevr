use gtk::gdk;

pub fn install() {
    let provider = gtk::CssProvider::new();

    provider.load_from_data(
        r#"
        .message-bubble {
            color: @window_fg_color;
            border-radius: 12px;
            padding: 8px 12px;
            border: 1px solid alpha(@window_fg_color, 0.12);
        }

        .message-bubble.incoming {
            background-color: @card_bg_color;
            border-color: alpha(@window_fg_color, 0.16);
            border-bottom-left-radius: 5px;
        }

        .message-bubble.outgoing {
            background-color: alpha(@accent_bg_color, 0.18);
            border-color: alpha(@accent_bg_color, 0.28);
            border-bottom-right-radius: 5px;
        }

        label.message-text {
            background-color: transparent;
            color: inherit;
            padding: 0;
        }

        .composer-input {
            border-radius: 16px;
            border: 1px solid alpha(@window_fg_color, 0.15);
            background-color: @card_bg_color;
        }

        .composer-input > * {
            border-radius: inherit;
            background-color: transparent;
        }

        .unread-badge {
            background-color: @accent_bg_color;
            color: @accent_fg_color;
            border-radius: 999px;
            font-weight: 700;
            padding: 2px 8px;
            min-width: 26px;
        }

        button.scroll-bottom-button {
            border-radius: 999px;
            padding: 8px;
            min-width: 40px;
            min-height: 40px;
            background-color: alpha(@accent_bg_color, 0.86);
            color: @accent_fg_color;
            box-shadow: 0 4px 16px alpha(black, 0.24);
        }

        button.scroll-bottom-button:hover {
            background-color: alpha(@accent_bg_color, 0.96);
        }

        .scroll-bottom-badge {
            color: inherit;
            font-weight: 800;
            font-size: 0.92em;
            padding: 0;
            min-width: 18px;
        }

        .older-messages-loading {
            border-radius: 999px;
            padding: 8px 12px;
            background-color: alpha(@window_bg_color, 0.88);
            box-shadow: 0 4px 16px alpha(black, 0.18);
        }

        picture.image-placeholder {
            background-color: alpha(@window_fg_color, 0.06);
            border-radius: 8px;
        }

        button.media-load-button {
            background-color: alpha(@window_fg_color, 0.06);
            border-radius: 8px;
            color: @window_fg_color;
        }

        .conversation-header-name {
            color: alpha(@window_fg_color, 0.82);
            font-weight: 700;
        }
    "#,
    );

    if let Some(display) = gdk::Display::default() {
        gtk::style_context_add_provider_for_display(
            &display,
            &provider,
            gtk::STYLE_PROVIDER_PRIORITY_APPLICATION,
        );
    }
}
